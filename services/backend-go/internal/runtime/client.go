package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

const DefaultBaseURL = "http://127.0.0.1:8000"

type Client struct {
	baseURL      string
	httpClient   *http.Client
	serviceToken string
}

// CallError carries a safe, machine-readable failure category back to the
// Backend service. Its message may retain transport detail for server logs,
// but public HTTP handlers intentionally keep the generic availability envelope.
type CallError struct {
	Code  string
	Stage string
	err   error
}

func (e *CallError) Error() string {
	if e.err == nil {
		return e.Code
	}
	return e.err.Error()
}

func (e *CallError) Unwrap() error { return e.err }

func (e *CallError) RuntimeFailure() (string, string) { return e.Code, e.Stage }

func NewClient(baseURL string, timeout time.Duration) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		trimmed = DefaultBaseURL
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{
		baseURL:      trimmed,
		serviceToken: strings.TrimSpace(os.Getenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN")),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) RunIngestion(ctx context.Context, file domain.KnowledgeFile, task domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	payload := map[string]any{
		"request_id":    fmt.Sprintf("req_ingest_%s", task.TaskID),
		"task_id":       task.TaskID,
		"file_id":       file.FileID,
		"filename":      file.Filename,
		"mime_type":     file.MIMEType,
		"source_format": file.SourceFormat,
		"object_uri":    file.ObjectURI,
		"parse_version": file.ParseVersion,
		"index_version": file.IndexVersion,
		"requested_at":  task.RequestedAt,
		"requested_by":  file.UploadedBy,
		"force_reindex": task.ForceReindex,
		"preview_only":  false,
	}
	if task.RevisionID != "" {
		payload["document_id"] = task.DocumentID
		payload["revision_id"] = task.RevisionID
		payload["delivery_id"] = task.DeliveryID
		payload["checksum_sha256"] = task.ChecksumSHA256
		payload["attempt"] = task.Attempt
	}

	var result domain.IngestionTaskResult
	if err := c.postJSON(ctx, "/v1/ingestion/run", payload, &result, "runtime ingestion"); err != nil {
		return domain.IngestionTaskResult{}, err
	}

	return result, nil
}

func (c *Client) RunChat(ctx context.Context, request domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	var result domain.ChatRuntimeResponse
	if err := c.postJSON(ctx, "/v1/chat/run", request, &result, "runtime chat"); err != nil {
		return domain.ChatRuntimeResponse{}, err
	}
	return result, nil
}

func (c *Client) StreamChat(ctx context.Context, request domain.ChatRuntimeRequest, emit func(domain.ChatRuntimeStreamEvent) error) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal runtime chat stream request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/stream", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create runtime chat stream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.setServiceAuthorization(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return runtimeTransportError(ctx, "call runtime chat stream endpoint", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		if readErr != nil {
			return fmt.Errorf("runtime chat stream failed with status %d", resp.StatusCode)
		}
		return runtimeHTTPError("runtime chat stream", resp.StatusCode, responseBody)
	}

	return readSSE(resp.Body, emit)
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, target any, operation string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", operation, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", operation, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.setServiceAuthorization(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return runtimeTransportError(ctx, "call "+operation+" endpoint", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		if readErr != nil {
			return fmt.Errorf("%s failed with status %d", operation, resp.StatusCode)
		}
		return runtimeHTTPError(operation, resp.StatusCode, responseBody)
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode %s response: %w", operation, err)
	}
	return nil
}

func runtimeTransportError(ctx context.Context, operation string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &CallError{Code: "backend_deadline_exceeded", Stage: "backend_runtime_deadline", err: fmt.Errorf("%s: %w", operation, ctx.Err())}
	}
	return &CallError{Code: "runtime_connection_failed", Stage: "runtime_transport", err: fmt.Errorf("%s: %w", operation, err)}
}

func runtimeHTTPError(operation string, statusCode int, body []byte) error {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	code := strings.TrimSpace(envelope.Error.Code)
	if code == "" {
		code = "runtime_http_error"
	}
	stage := "runtime_http"
	if strings.HasPrefix(code, "model_") {
		stage = "model_provider"
	}
	return &CallError{Code: code, Stage: stage, err: fmt.Errorf("%s failed with status %d: %s", operation, statusCode, strings.TrimSpace(string(body)))}
}

func (c *Client) setServiceAuthorization(req *http.Request) {
	if c.serviceToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.serviceToken)
	}
}

func readSSE(reader io.Reader, emit func(domain.ChatRuntimeStreamEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventName := "message"
	dataLines := []string{}

	flush := func() error {
		if len(dataLines) == 0 {
			eventName = "message"
			return nil
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = []string{}
		currentEvent := eventName
		eventName = "message"
		event, err := decodeStreamEvent(currentEvent, []byte(payload))
		if err != nil {
			return err
		}
		return emit(event)
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read runtime chat stream: %w", err)
	}
	return flush()
}

func decodeStreamEvent(eventName string, payload []byte) (domain.ChatRuntimeStreamEvent, error) {
	switch eventName {
	case "message.delta":
		var item struct {
			RequestID string `json:"request_id"`
			MessageID string `json:"message_id"`
			Delta     string `json:"delta"`
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			return domain.ChatRuntimeStreamEvent{}, fmt.Errorf("decode runtime stream delta: %w", err)
		}
		return domain.ChatRuntimeStreamEvent{
			Event:     eventName,
			RequestID: item.RequestID,
			MessageID: item.MessageID,
			Delta:     item.Delta,
		}, nil
	case "message.completed":
		var item struct {
			RequestID           string                     `json:"request_id"`
			MessageID           string                     `json:"message_id"`
			ChatRuntimeResponse domain.ChatRuntimeResponse `json:"chat_runtime_response"`
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			return domain.ChatRuntimeStreamEvent{}, fmt.Errorf("decode runtime stream completed: %w", err)
		}
		return domain.ChatRuntimeStreamEvent{
			Event:     eventName,
			RequestID: item.RequestID,
			MessageID: item.MessageID,
			Response:  &item.ChatRuntimeResponse,
		}, nil
	case "message.error":
		var item struct {
			RequestID string                         `json:"request_id"`
			MessageID string                         `json:"message_id"`
			Error     *domain.ChatRuntimeStreamError `json:"error"`
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			return domain.ChatRuntimeStreamEvent{}, fmt.Errorf("decode runtime stream error: %w", err)
		}
		return domain.ChatRuntimeStreamEvent{
			Event:     eventName,
			RequestID: item.RequestID,
			MessageID: item.MessageID,
			Error:     item.Error,
		}, nil
	default:
		return domain.ChatRuntimeStreamEvent{Event: eventName}, nil
	}
}
