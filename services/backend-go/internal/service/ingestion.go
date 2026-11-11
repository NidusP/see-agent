package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
	"see-agent/services/backend-go/internal/taskqueue"
)

const ingestionDispatchTimeout = 30 * time.Second
const ingestionEnqueueTimeout = 5 * time.Second
const ingestionQueueRetryDelay = 250 * time.Millisecond

var ErrIngestionQueueUnavailable = errors.New("ingestion queue unavailable")

func (s *Service) createFileWithDispatch(filename, mimeType string, content []byte, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary, error) {
	fileID := s.store.ReserveFileID()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	objectURI, err := s.uploadStore.Put(ctx, fileID, filename, mimeType, content)
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, err
	}

	file, task := s.store.CreateFile(fileID, filename, mimeType, objectURI, forceReindex)
	if err := s.enqueueIngestion(file, task); err != nil {
		s.failIngestionTask(file.FileID, task.TaskID, "queue_unavailable", err.Error(), true)
		return file, task, err
	}
	return file, task, nil
}

func (s *Service) reindexFileWithDispatch(fileID string, forceReindex bool) (domain.IngestionTaskSummary, bool, error) {
	file, task, ok := s.store.ReindexFile(fileID, forceReindex)
	if !ok {
		return domain.IngestionTaskSummary{}, false, nil
	}
	if err := s.enqueueIngestion(file, task); err != nil {
		s.failIngestionTask(file.FileID, task.TaskID, "queue_unavailable", err.Error(), true)
		return task, true, err
	}
	return task, true, nil
}

func (s *Service) enqueueIngestion(file domain.KnowledgeFile, task domain.IngestionTaskSummary) error {
	if s.ingestionQueue == nil {
		return fmt.Errorf("%w: queue is not configured", ErrIngestionQueueUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), ingestionEnqueueTimeout)
	defer cancel()
	if err := s.ingestionQueue.Enqueue(ctx, taskqueue.IngestionJob{FileID: file.FileID, TaskID: task.TaskID}); err != nil {
		return fmt.Errorf("%w: %v", ErrIngestionQueueUnavailable, err)
	}
	return nil
}

func (s *Service) startIngestionWorker() {
	if s.ingestionQueue == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.workerCancel = cancel
	go func() {
		for {
			job, err := s.ingestionQueue.Dequeue(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, taskqueue.ErrClosed) {
					return
				}
				time.Sleep(ingestionQueueRetryDelay)
				continue
			}
			s.runIngestionJob(job)
		}
	}()
}

func (s *Service) runIngestionJob(job taskqueue.IngestionJob) {
	if job.FileID == "" || job.TaskID == "" {
		return
	}
	file, task, ok := s.store.GetFile(job.FileID)
	if !ok || task.TaskID != job.TaskID {
		return
	}

	if ok := s.store.MarkTaskRunning(file.FileID, task.TaskID); !ok {
		return
	}

	if s.runtimeClient == nil {
		s.failIngestionTask(file.FileID, task.TaskID, "runtime_unavailable", "runtime client is not configured", true)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), ingestionDispatchTimeout)
	defer cancel()

	result, err := s.runtimeClient.RunIngestion(ctx, file, task)
	if err != nil {
		s.failIngestionTask(file.FileID, task.TaskID, "runtime_unavailable", err.Error(), true)
		return
	}

	if ok := s.store.ApplyIngestionResult(file.FileID, task.TaskID, result); !ok {
		s.failIngestionStateWritebackFailure(file.FileID, task.TaskID)
	}
}

func (s *Service) failIngestionStateWritebackFailure(fileID, taskID string) {
	s.failIngestionTask(fileID, taskID, "state_writeback_failed", "failed to apply runtime ingestion result", false)
}

func (s *Service) failIngestionTask(fileID, taskID, code, message string, retryable bool) {
	_ = s.store.ApplyIngestionResult(fileID, taskID, domain.IngestionTaskResult{
		TaskID:    taskID,
		FileID:    fileID,
		Status:    "failed",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Error: &domain.IngestionTaskError{
			Code:      code,
			Message:   message,
			Retryable: retryable,
		},
	})
}

const (
	defaultRevisionDeliveryLease       = 30 * time.Second
	defaultRevisionDeliveryReadBlock   = 500 * time.Millisecond
	defaultRevisionDeliveryRetryBase   = time.Second
	defaultRevisionDeliveryRetryMax    = 5 * time.Minute
	defaultRevisionDeliveryMaxAttempts = 5
	// The outbox relay intentionally XADDs before it records `streamed`: a
	// crash in that interval is recoverable. A fast consumer can observe that
	// short interval, though, so it must not ACK the message as stale.
	defaultRevisionDeliveryTransitionRetryDelay = 25 * time.Millisecond
	defaultRevisionDeliveryTransitionRetries    = 4
)

// RevisionIngestionRequest is the Backend-owned view of a K3 revision task.
// It requires the Runtime adapter to preserve all immutable identity fields.
type RevisionIngestionRequest struct {
	TaskID         string
	FileID         string
	DeliveryID     string
	DocumentID     string
	RevisionID     string
	ChecksumSHA256 string
	Attempt        int
	Filename       string
	MIMEType       string
	SourceFormat   string
	ObjectURI      string
	ParseVersion   string
	IndexVersion   string
	RequestedAt    string
}

type RevisionIngestionResult struct {
	DeliveryID          string
	DocumentID          string
	RevisionID          string
	ChecksumSHA256      string
	Status              string
	ChunkManifestSHA256 string
	EmbeddingModel      string
	Error               *domain.IngestionTaskError
}

// RevisionRuntimeClient is intentionally separate from the legacy RuntimeClient.
// K3 callers must not accidentally send a revision task through the file/List path.
type RevisionRuntimeClient interface {
	RunRevisionIngestion(ctx context.Context, request RevisionIngestionRequest) (RevisionIngestionResult, error)
}

type legacyIngestionRuntime interface {
	RunIngestion(ctx context.Context, file domain.KnowledgeFile, task domain.IngestionTaskSummary) (domain.IngestionTaskResult, error)
}

type revisionRuntimeAdapter struct {
	client legacyIngestionRuntime
}

// NewRevisionRuntimeAdapter prevents K3 delivery work from falling back to the
// legacy List semantics while still using the established Runtime HTTP client.
func NewRevisionRuntimeAdapter(client legacyIngestionRuntime) (RevisionRuntimeClient, error) {
	if client == nil {
		return nil, errors.New("legacy runtime ingestion client is required")
	}
	return revisionRuntimeAdapter{client: client}, nil
}

func (a revisionRuntimeAdapter) RunRevisionIngestion(ctx context.Context, request RevisionIngestionRequest) (RevisionIngestionResult, error) {
	result, err := a.client.RunIngestion(ctx, domain.KnowledgeFile{
		FileID:       request.FileID,
		Filename:     request.Filename,
		MIMEType:     request.MIMEType,
		SourceFormat: request.SourceFormat,
		ObjectURI:    request.ObjectURI,
		ParseVersion: request.ParseVersion,
		IndexVersion: request.IndexVersion,
	}, domain.IngestionTaskSummary{
		TaskID:         request.TaskID,
		FileID:         request.FileID,
		DocumentID:     request.DocumentID,
		RevisionID:     request.RevisionID,
		DeliveryID:     request.DeliveryID,
		ChecksumSHA256: request.ChecksumSHA256,
		Attempt:        request.Attempt,
		RequestedAt:    request.RequestedAt,
	})
	if err != nil {
		return RevisionIngestionResult{}, err
	}
	if result.DocumentID != request.DocumentID || result.RevisionID != request.RevisionID || result.DeliveryID != request.DeliveryID {
		return RevisionIngestionResult{}, fmt.Errorf("%w: runtime result document/revision/delivery does not match request", repository.ErrKnowledgeDeliveryIdentityMismatch)
	}
	return RevisionIngestionResult{
		DeliveryID: result.DeliveryID,
		DocumentID: result.DocumentID,
		RevisionID: result.RevisionID,
		// The frozen Runtime result DTO does not echo checksum yet. It is only
		// filled after response identity matches this immutable request; the
		// repository still checks it before transitioning to ready.
		ChecksumSHA256:      request.ChecksumSHA256,
		Status:              result.Status,
		ChunkManifestSHA256: result.ChunkManifestSHA256,
		EmbeddingModel:      result.EmbeddingModel,
		Error:               result.Error,
	}, nil
}

// RevisionDeliveryWorker consumes only the K3 Redis Stream. It never observes
// or acknowledges the legacy Redis List used by existing file ingestion.
type RevisionDeliveryWorker struct {
	store       repository.KnowledgeDeliveryStore
	stream      taskqueue.RevisionStream
	runtime     RevisionRuntimeClient
	lease       time.Duration
	readBlock   time.Duration
	retryBase   time.Duration
	retryMax    time.Duration
	maxAttempts int
}

func NewRevisionDeliveryWorker(store repository.KnowledgeDeliveryStore, stream taskqueue.RevisionStream, runtimeClient RevisionRuntimeClient) (*RevisionDeliveryWorker, error) {
	if store == nil {
		return nil, errors.New("knowledge delivery store is required")
	}
	if stream == nil {
		return nil, errors.New("revision stream is required")
	}
	if runtimeClient == nil {
		return nil, errors.New("revision runtime client is required")
	}
	return &RevisionDeliveryWorker{
		store:       store,
		stream:      stream,
		runtime:     runtimeClient,
		lease:       defaultRevisionDeliveryLease,
		readBlock:   defaultRevisionDeliveryReadBlock,
		retryBase:   defaultRevisionDeliveryRetryBase,
		retryMax:    defaultRevisionDeliveryRetryMax,
		maxAttempts: defaultRevisionDeliveryMaxAttempts,
	}, nil
}

// Run blocks until the context ends. The caller owns process lifecycle and may
// run the K3 worker beside the legacy Service ingestion worker during migration.
func (w *RevisionDeliveryWorker) Run(ctx context.Context) error {
	for {
		if err := w.RunOnce(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(ingestionQueueRetryDelay):
			}
		}
	}
}

// RunOnce first reclaims expired PEL entries, then receives new messages. A
// failed database transition leaves the message pending for the next reclaim.
func (w *RevisionDeliveryWorker) RunOnce(ctx context.Context) error {
	if err := w.stream.EnsureGroup(ctx); err != nil {
		return err
	}
	messages, err := w.stream.Reclaim(ctx, w.lease, 10)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		messages, err = w.stream.Read(ctx, 10, w.readBlock)
		if err != nil {
			return err
		}
	}
	for _, message := range messages {
		if err := w.processMessage(ctx, message); err != nil {
			return err
		}
	}
	return nil
}

func (w *RevisionDeliveryWorker) processMessage(ctx context.Context, message taskqueue.RevisionStreamMessage) error {
	delivery, claimed, err := w.claimMessageAfterRelayTransition(ctx, message)
	if err != nil {
		if errors.Is(err, repository.ErrKnowledgeDeliveryLeaseActive) {
			return nil
		}
		return err
	}
	if !claimed {
		return w.stream.Ack(ctx, message.StreamMessageID)
	}

	result, err := w.runtime.RunRevisionIngestion(ctx, revisionIngestionRequest(delivery))
	if err != nil {
		return w.handleFailure(ctx, message, delivery, err.Error(), retryableRuntimeError(err))
	}
	if strings.TrimSpace(result.Status) != "succeeded" {
		failure := "runtime returned non-success result"
		retryable := false
		if result.Error != nil {
			failure = strings.TrimSpace(result.Error.Code + ": " + result.Error.Message)
			retryable = result.Error.Retryable
		}
		return w.handleFailure(ctx, message, delivery, failure, retryable)
	}

	_, err = w.store.CompleteKnowledgeRevisionDelivery(ctx, message.StreamMessageID, repository.KnowledgeDeliveryResult{
		DeliveryID:          result.DeliveryID,
		DocumentID:          result.DocumentID,
		RevisionID:          result.RevisionID,
		ChecksumSHA256:      result.ChecksumSHA256,
		ChunkManifestSHA256: result.ChunkManifestSHA256,
		EmbeddingModel:      result.EmbeddingModel,
	})
	if err != nil {
		if errors.Is(err, repository.ErrKnowledgeDeliveryIdentityMismatch) {
			return w.handleFailure(ctx, message, delivery, err.Error(), false)
		}
		return err
	}
	return w.stream.Ack(ctx, message.StreamMessageID)
}

// claimMessageAfterRelayTransition closes the intentional XADD -> streamed
// write race. It retries briefly while the authoritative outbox is still
// pending/retry and otherwise leaves the Stream entry pending for reclaim;
// acknowledging it there would silently strand a queued revision forever.
func (w *RevisionDeliveryWorker) claimMessageAfterRelayTransition(ctx context.Context, message taskqueue.RevisionStreamMessage) (repository.KnowledgeDelivery, bool, error) {
	for attempt := 0; attempt < defaultRevisionDeliveryTransitionRetries; attempt++ {
		delivery, claimed, err := w.store.ClaimKnowledgeRevisionDelivery(ctx, message.DeliveryID, message.StreamMessageID, w.lease)
		if err != nil || claimed || (delivery.Status != "pending" && delivery.Status != "retry") {
			return delivery, claimed, err
		}
		select {
		case <-ctx.Done():
			return delivery, false, ctx.Err()
		case <-time.After(defaultRevisionDeliveryTransitionRetryDelay):
		}
	}
	return repository.KnowledgeDelivery{}, false, fmt.Errorf("revision delivery %s was not marked streamed after redis publication", message.DeliveryID)
}

func (w *RevisionDeliveryWorker) handleFailure(ctx context.Context, message taskqueue.RevisionStreamMessage, delivery repository.KnowledgeDelivery, failure string, retryable bool) error {
	if retryable && delivery.AttemptCount < w.maxAttempts {
		_, err := w.store.ScheduleKnowledgeRevisionDeliveryRetry(ctx, delivery.DeliveryID, message.StreamMessageID, failure, time.Now().UTC().Add(w.retryDelay(delivery.AttemptCount)))
		if err != nil {
			return err
		}
		return w.stream.Ack(ctx, message.StreamMessageID)
	}
	if _, err := w.stream.DeadLetter(ctx, message.RevisionStreamEvent, message.StreamMessageID, failure); err != nil {
		return err
	}
	if _, err := w.store.DeadLetterKnowledgeRevisionDelivery(ctx, delivery.DeliveryID, message.StreamMessageID, failure); err != nil {
		return err
	}
	return w.stream.Ack(ctx, message.StreamMessageID)
}

func (w *RevisionDeliveryWorker) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := w.retryBase
	for current := 1; current < attempt && delay < w.retryMax; current++ {
		delay *= 2
	}
	if delay > w.retryMax {
		return w.retryMax
	}
	return delay
}

func revisionIngestionRequest(delivery repository.KnowledgeDelivery) RevisionIngestionRequest {
	fileID := delivery.Revision.LegacyFileID
	if fileID == "" {
		// K3 revisions need not originate from legacy files; revision_id is the
		// stable compatibility value required by the existing Runtime DTO.
		fileID = delivery.RevisionID
	}
	return RevisionIngestionRequest{
		TaskID:         delivery.DeliveryID,
		FileID:         fileID,
		DeliveryID:     delivery.DeliveryID,
		DocumentID:     delivery.DocumentID,
		RevisionID:     delivery.RevisionID,
		ChecksumSHA256: delivery.Revision.ChecksumSHA256,
		Attempt:        delivery.AttemptCount,
		Filename:       delivery.Revision.Filename,
		MIMEType:       delivery.Revision.MIMEType,
		SourceFormat:   delivery.Revision.SourceFormat,
		ObjectURI:      delivery.Revision.ObjectURI,
		ParseVersion:   delivery.Revision.ParseVersion,
		IndexVersion:   delivery.Revision.IndexVersion,
		RequestedAt:    delivery.AvailableAt.UTC().Format(time.RFC3339),
	}
}

type retryableRuntime interface {
	Retryable() bool
}

func retryableRuntimeError(err error) bool {
	if retryable, ok := err.(retryableRuntime); ok {
		return retryable.Retryable()
	}
	return true
}

// StartRevisionDeliveryPipeline owns both halves of K3 delivery. It starts the
// outbox relay and stream consumer together, and returns one shutdown function.
func StartRevisionDeliveryPipeline(parent context.Context, store repository.KnowledgeDeliveryStore, stream taskqueue.RevisionStream, runtimeClient RevisionRuntimeClient, relayInterval time.Duration) (func(), error) {
	if relayInterval <= 0 {
		relayInterval = time.Second
	}
	worker, err := NewRevisionDeliveryWorker(store, stream, runtimeClient)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("K3 revision worker stopped: %v", err)
		}
	}()
	go func() {
		relay := func() {
			if _, err := repository.RelayKnowledgeRevisionOutbox(ctx, store, stream, 50); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("K3 revision outbox relay failed: %v", err)
			}
		}
		relay()
		ticker := time.NewTicker(relayInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				relay()
			}
		}
	}()
	return func() {
		cancel()
		_ = stream.Close()
	}, nil
}
