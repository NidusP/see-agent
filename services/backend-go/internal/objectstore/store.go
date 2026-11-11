package objectstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Store interface {
	Put(ctx context.Context, fileID, filename, contentType string, content []byte) (string, error)
}

type LocalStore struct {
	Root string
}

func (s LocalStore) Put(ctx context.Context, fileID, filename, _ string, content []byte) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	root := strings.TrimSpace(s.Root)
	if root == "" {
		root = filepath.Join(os.TempDir(), "see-agent-backend-uploads")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create upload root: %w", err)
	}

	spoolDir, err := os.MkdirTemp(root, "kb-*")
	if err != nil {
		return "", fmt.Errorf("create upload spool dir: %w", err)
	}

	targetPath := filepath.Join(spoolDir, safeFilename(filename))
	if err := os.WriteFile(targetPath, content, 0o644); err != nil {
		return "", fmt.Errorf("write upload spool file: %w", err)
	}
	return targetPath, nil
}

type S3Config struct {
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	Timeout   time.Duration
}

type S3Store struct {
	config S3Config
	client *http.Client
	now    func() time.Time
}

func NewS3Store(config S3Config, client *http.Client) *S3Store {
	if strings.TrimSpace(config.Region) == "" {
		config.Region = "us-east-1"
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if client == nil {
		client = &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				Proxy: nil,
			},
		}
	}
	return &S3Store{
		config: config,
		client: client,
		now:    time.Now,
	}
}

func (s *S3Store) Put(ctx context.Context, fileID, filename, contentType string, content []byte) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	if err := s.ensureBucket(ctx); err != nil {
		return "", err
	}

	key := fmt.Sprintf("kb/%s/%s", sanitizePathSegment(fileID), safeFilename(filename))
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	req, err := s.newSignedRequest(ctx, http.MethodPut, s.config.Bucket, key, bytes.NewReader(content), int64(len(content)), contentType)
	if err != nil {
		return "", fmt.Errorf("create object upload request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload object to object store: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", fmt.Errorf("upload object failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return fmt.Sprintf("s3://%s/%s", s.config.Bucket, key), nil
}

func (s *S3Store) ensureBucket(ctx context.Context) error {
	headReq, err := s.newSignedRequest(ctx, http.MethodHead, s.config.Bucket, "", nil, 0, "")
	if err != nil {
		return fmt.Errorf("create bucket check request: %w", err)
	}
	headResp, err := s.client.Do(headReq)
	if err != nil {
		return fmt.Errorf("check object store bucket: %w", err)
	}
	headResp.Body.Close()

	if headResp.StatusCode >= http.StatusOK && headResp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	if headResp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("check object store bucket failed with status %d", headResp.StatusCode)
	}

	putReq, err := s.newSignedRequest(ctx, http.MethodPut, s.config.Bucket, "", http.NoBody, 0, "")
	if err != nil {
		return fmt.Errorf("create bucket request: %w", err)
	}
	putResp, err := s.client.Do(putReq)
	if err != nil {
		return fmt.Errorf("create object store bucket: %w", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode == http.StatusConflict {
		return nil
	}
	if putResp.StatusCode < http.StatusOK || putResp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(putResp.Body, 8<<10))
		return fmt.Errorf("create object store bucket failed with status %d: %s", putResp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *S3Store) newSignedRequest(ctx context.Context, method, bucket, key string, body io.Reader, contentLength int64, contentType string) (*http.Request, error) {
	endpointURL, err := s.objectURL(bucket, key)
	if err != nil {
		return nil, err
	}
	payloadHash := emptyPayloadHash
	if body != nil && contentLength > 0 {
		if readSeeker, ok := body.(io.ReadSeeker); ok {
			payload, err := io.ReadAll(readSeeker)
			if err != nil {
				return nil, err
			}
			payloadHash = sha256Hex(payload)
			if _, err := readSeeker.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, endpointURL.String(), body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = contentLength
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	s.sign(req, payloadHash)
	return req, nil
}

func (s *S3Store) objectURL(bucket, key string) (*url.URL, error) {
	endpointURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(s.config.Endpoint), "/"))
	if err != nil {
		return nil, fmt.Errorf("parse object store endpoint: %w", err)
	}
	if endpointURL.Scheme == "" || endpointURL.Host == "" {
		return nil, fmt.Errorf("object store endpoint must include scheme and host")
	}

	segments := []string{strings.Trim(endpointURL.Path, "/"), bucket}
	if key != "" {
		segments = append(segments, strings.Split(key, "/")...)
	}
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(segment))
	}
	endpointURL.Path = "/" + strings.Join(escaped, "/")
	endpointURL.RawQuery = ""
	return endpointURL, nil
}

func (s *S3Store) sign(req *http.Request, payloadHash string) {
	now := s.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := map[string]string{
		"host":                 req.URL.Host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	if contentType := req.Header.Get("Content-Type"); contentType != "" {
		headers["content-type"] = contentType
	}

	headerNames := make([]string, 0, len(headers))
	for name := range headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)

	var canonicalHeaders strings.Builder
	for _, name := range headerNames {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(headers[name]))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(headerNames, ";")
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.config.Region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(s.config.SecretKey, dateStamp, s.config.Region), []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.config.AccessKey,
		scope,
		signedHeaders,
		signature,
	))
}

func (s *S3Store) validate() error {
	if strings.TrimSpace(s.config.Endpoint) == "" {
		return fmt.Errorf("object store endpoint is required")
	}
	if strings.TrimSpace(s.config.Bucket) == "" {
		return fmt.Errorf("object store bucket is required")
	}
	if strings.TrimSpace(s.config.AccessKey) == "" || strings.TrimSpace(s.config.SecretKey) == "" {
		return fmt.Errorf("object store credentials are required")
	}
	return nil
}

func FromEnv(uploadRoot string) Store {
	endpoint := strings.TrimSpace(os.Getenv("OBJECT_STORE_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("OBJECT_STORE_BUCKET"))
	if endpoint == "" || bucket == "" {
		return LocalStore{Root: uploadRoot}
	}

	accessKey := firstNonEmpty(os.Getenv("OBJECT_STORE_ACCESS_KEY"), os.Getenv("MINIO_ROOT_USER"), "see_agent")
	secretKey := firstNonEmpty(os.Getenv("OBJECT_STORE_SECRET_KEY"), os.Getenv("MINIO_ROOT_PASSWORD"), "see_agent_dev")
	region := firstNonEmpty(os.Getenv("OBJECT_STORE_REGION"), "us-east-1")
	return NewS3Store(S3Config{
		Endpoint:  endpoint,
		Bucket:    bucket,
		Region:    region,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Timeout:   10 * time.Second,
	}, nil)
}

func safeFilename(filename string) string {
	name := filepath.Base(strings.TrimSpace(filename))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "upload.bin"
	}
	return sanitizePathSegment(name)
}

func sanitizePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "upload"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	sanitized := strings.Trim(builder.String(), ".-")
	if sanitized == "" {
		return "upload"
	}
	return sanitized
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func signingKey(secret, dateStamp, region string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	regionKey := hmacSHA256(dateKey, []byte(region))
	serviceKey := hmacSHA256(regionKey, []byte("s3"))
	return hmacSHA256(serviceKey, []byte("aws4_request"))
}

func hmacSHA256(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return mac.Sum(nil)
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
