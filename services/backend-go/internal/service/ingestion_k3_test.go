package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
	"see-agent/services/backend-go/internal/taskqueue"
)

func TestRevisionDeliveryWorkerCompletesMatchingIdentity(t *testing.T) {
	store := &deliveryStoreStub{delivery: testDelivery(1), claimed: true}
	stream := &revisionStreamStub{reclaimed: []taskqueue.RevisionStreamMessage{testStreamMessage()}}
	runtime := &revisionRuntimeStub{result: matchingRevisionResult()}
	worker := newTestRevisionDeliveryWorker(t, store, stream, runtime)

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !store.completed {
		t.Fatal("expected matching result to complete delivery")
	}
	if len(stream.acked) != 1 || stream.acked[0] != "1710000000000-0" {
		t.Fatalf("acked = %#v", stream.acked)
	}
	if stream.deadLettered {
		t.Fatal("successful result must not be dead-lettered")
	}
}

func TestRevisionDeliveryWorkerDeadLettersIdentityMismatch(t *testing.T) {
	store := &deliveryStoreStub{delivery: testDelivery(1), claimed: true, completeErr: repository.ErrKnowledgeDeliveryIdentityMismatch}
	stream := &revisionStreamStub{reclaimed: []taskqueue.RevisionStreamMessage{testStreamMessage()}}
	runtime := &revisionRuntimeStub{result: matchingRevisionResult()}
	worker := newTestRevisionDeliveryWorker(t, store, stream, runtime)

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !stream.deadLettered || !store.deadLettered {
		t.Fatalf("expected identity mismatch to dead-letter: stream=%v store=%v", stream.deadLettered, store.deadLettered)
	}
	if len(stream.acked) != 1 {
		t.Fatalf("acked = %#v", stream.acked)
	}
}

func TestRevisionDeliveryWorkerRetriesRetryableFailure(t *testing.T) {
	store := &deliveryStoreStub{delivery: testDelivery(1), claimed: true}
	stream := &revisionStreamStub{reclaimed: []taskqueue.RevisionStreamMessage{testStreamMessage()}}
	runtime := &revisionRuntimeStub{result: RevisionIngestionResult{Status: "failed", Error: &domain.IngestionTaskError{Code: "unavailable", Message: "runtime offline", Retryable: true}}}
	worker := newTestRevisionDeliveryWorker(t, store, stream, runtime)

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !store.retried || store.deadLettered || stream.deadLettered {
		t.Fatalf("retry path state: retried=%v storeDead=%v streamDead=%v", store.retried, store.deadLettered, stream.deadLettered)
	}
	if len(stream.acked) != 1 {
		t.Fatalf("acked = %#v", stream.acked)
	}
}

func TestRevisionDeliveryWorkerAcksStaleMessageWithoutRuntimeCall(t *testing.T) {
	store := &deliveryStoreStub{delivery: testDelivery(1), claimed: false}
	stream := &revisionStreamStub{reclaimed: []taskqueue.RevisionStreamMessage{testStreamMessage()}}
	runtime := &revisionRuntimeStub{result: matchingRevisionResult()}
	worker := newTestRevisionDeliveryWorker(t, store, stream, runtime)

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if runtime.calls != 0 {
		t.Fatalf("runtime calls = %d, want 0", runtime.calls)
	}
	if len(stream.acked) != 1 {
		t.Fatalf("acked = %#v", stream.acked)
	}
}

func TestRevisionDeliveryWorkerDoesNotAckBeforeRelayMarksStreamed(t *testing.T) {
	pending := testDelivery(0)
	pending.Status = "pending"
	store := &deliveryStoreStub{
		delivery: testDelivery(1),
		claimResponses: []deliveryClaimResponse{
			{delivery: pending, claimed: false},
			{delivery: testDelivery(1), claimed: true},
		},
	}
	stream := &revisionStreamStub{reclaimed: []taskqueue.RevisionStreamMessage{testStreamMessage()}}
	runtime := &revisionRuntimeStub{result: matchingRevisionResult()}
	worker := newTestRevisionDeliveryWorker(t, store, stream, runtime)

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if store.claimCalls != 2 || runtime.calls != 1 || !store.completed {
		t.Fatalf("relay transition was not retried safely: claims=%d runtime=%d completed=%v", store.claimCalls, runtime.calls, store.completed)
	}
	if len(stream.acked) != 1 {
		t.Fatalf("acked = %#v", stream.acked)
	}
}

func TestRevisionRuntimeAdapterPreservesImmutableIdentity(t *testing.T) {
	legacy := &legacyRuntimeStub{result: domain.IngestionTaskResult{
		DocumentID:          "doc_001",
		RevisionID:          "rev_001",
		DeliveryID:          "delivery_001",
		Status:              "succeeded",
		ChunkManifestSHA256: "sha256:chunks",
		EmbeddingModel:      "bge-m3",
	}}
	adapter, err := NewRevisionRuntimeAdapter(legacy)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	result, err := adapter.RunRevisionIngestion(context.Background(), RevisionIngestionRequest{
		TaskID:         "delivery_001",
		FileID:         "rev_001",
		DeliveryID:     "delivery_001",
		DocumentID:     "doc_001",
		RevisionID:     "rev_001",
		ChecksumSHA256: "sha256:policy",
		Attempt:        2,
		Filename:       "policy.md",
		MIMEType:       "text/markdown",
		SourceFormat:   "md",
		ObjectURI:      "s3://knowledge-docs/policy.md",
		ParseVersion:   "parse_v1",
		IndexVersion:   "index_v1",
		RequestedAt:    "2026-07-14T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("run adapter: %v", err)
	}
	if legacy.task.ChecksumSHA256 != "sha256:policy" || legacy.task.Attempt != 2 || legacy.task.DeliveryID != "delivery_001" {
		t.Fatalf("runtime task identity = %#v", legacy.task)
	}
	if result.ChecksumSHA256 != "sha256:policy" || result.ChunkManifestSHA256 != "sha256:chunks" {
		t.Fatalf("adapter result = %#v", result)
	}
}

func TestRevisionRuntimeAdapterRejectsResponseIdentityMismatch(t *testing.T) {
	legacy := &legacyRuntimeStub{result: domain.IngestionTaskResult{DocumentID: "doc_other", RevisionID: "rev_001", DeliveryID: "delivery_001", Status: "succeeded"}}
	adapter, err := NewRevisionRuntimeAdapter(legacy)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	_, err = adapter.RunRevisionIngestion(context.Background(), RevisionIngestionRequest{TaskID: "delivery_001", FileID: "rev_001", DeliveryID: "delivery_001", DocumentID: "doc_001", RevisionID: "rev_001", ChecksumSHA256: "sha256:policy"})
	if !errors.Is(err, repository.ErrKnowledgeDeliveryIdentityMismatch) {
		t.Fatalf("identity mismatch error = %v", err)
	}
}

func newTestRevisionDeliveryWorker(t *testing.T, store repository.KnowledgeDeliveryStore, stream taskqueue.RevisionStream, runtime RevisionRuntimeClient) *RevisionDeliveryWorker {
	t.Helper()
	worker, err := NewRevisionDeliveryWorker(store, stream, runtime)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	worker.readBlock = 0
	return worker
}

func testDelivery(attempt int) repository.KnowledgeDelivery {
	return repository.KnowledgeDelivery{
		DeliveryID:      "delivery_001",
		DocumentID:      "doc_001",
		RevisionID:      "rev_001",
		Status:          "indexing",
		AttemptCount:    attempt,
		StreamMessageID: "1710000000000-0",
		AvailableAt:     time.Now().UTC(),
		Revision: domain.KnowledgeRevision{
			RevisionID:     "rev_001",
			DocumentID:     "doc_001",
			Filename:       "policy.md",
			MIMEType:       "text/markdown",
			SourceFormat:   "md",
			ObjectURI:      "s3://knowledge-docs/policy.md",
			ChecksumSHA256: "sha256:policy",
			ParseVersion:   "parse_v1",
			IndexVersion:   "index_v1",
			Status:         "indexing",
		},
	}
}

func testStreamMessage() taskqueue.RevisionStreamMessage {
	return taskqueue.RevisionStreamMessage{
		StreamMessageID: "1710000000000-0",
		RevisionStreamEvent: taskqueue.RevisionStreamEvent{
			OutboxID:       "delivery_001",
			DeliveryID:     "delivery_001",
			DocumentID:     "doc_001",
			RevisionID:     "rev_001",
			ChecksumSHA256: "sha256:policy",
			Attempt:        0,
		},
	}
}

func matchingRevisionResult() RevisionIngestionResult {
	return RevisionIngestionResult{
		DeliveryID:          "delivery_001",
		DocumentID:          "doc_001",
		RevisionID:          "rev_001",
		ChecksumSHA256:      "sha256:policy",
		Status:              "succeeded",
		ChunkManifestSHA256: "sha256:chunks",
		EmbeddingModel:      "bge-m3",
	}
}

type revisionRuntimeStub struct {
	result RevisionIngestionResult
	err    error
	calls  int
}

func (s *revisionRuntimeStub) RunRevisionIngestion(context.Context, RevisionIngestionRequest) (RevisionIngestionResult, error) {
	s.calls++
	return s.result, s.err
}

type legacyRuntimeStub struct {
	result domain.IngestionTaskResult
	err    error
	file   domain.KnowledgeFile
	task   domain.IngestionTaskSummary
}

func (s *legacyRuntimeStub) RunIngestion(_ context.Context, file domain.KnowledgeFile, task domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	s.file = file
	s.task = task
	return s.result, s.err
}

type deliveryStoreStub struct {
	delivery       repository.KnowledgeDelivery
	claimed        bool
	claimResponses []deliveryClaimResponse
	claimCalls     int
	completeErr    error
	completed      bool
	retried        bool
	deadLettered   bool
}

type deliveryClaimResponse struct {
	delivery repository.KnowledgeDelivery
	claimed  bool
	err      error
}

func (s *deliveryStoreStub) ListPendingKnowledgeRevisionDeliveries(context.Context, int) ([]repository.KnowledgeDelivery, error) {
	return nil, nil
}

func (s *deliveryStoreStub) MarkKnowledgeDeliveryStreamed(context.Context, string, string) (repository.KnowledgeDelivery, error) {
	return s.delivery, nil
}

func (s *deliveryStoreStub) ClaimKnowledgeRevisionDelivery(context.Context, string, string, time.Duration) (repository.KnowledgeDelivery, bool, error) {
	s.claimCalls++
	if len(s.claimResponses) > 0 {
		response := s.claimResponses[0]
		s.claimResponses = s.claimResponses[1:]
		return response.delivery, response.claimed, response.err
	}
	if !s.claimed {
		return s.delivery, false, nil
	}
	return s.delivery, true, nil
}

func (s *deliveryStoreStub) CompleteKnowledgeRevisionDelivery(context.Context, string, repository.KnowledgeDeliveryResult) (repository.KnowledgeDelivery, error) {
	if s.completeErr != nil {
		return repository.KnowledgeDelivery{}, s.completeErr
	}
	s.completed = true
	return s.delivery, nil
}

func (s *deliveryStoreStub) ScheduleKnowledgeRevisionDeliveryRetry(context.Context, string, string, string, time.Time) (repository.KnowledgeDelivery, error) {
	s.retried = true
	return s.delivery, nil
}

func (s *deliveryStoreStub) DeadLetterKnowledgeRevisionDelivery(context.Context, string, string, string) (repository.KnowledgeDelivery, error) {
	s.deadLettered = true
	return s.delivery, nil
}

type revisionStreamStub struct {
	reclaimed    []taskqueue.RevisionStreamMessage
	acked        []string
	deadLettered bool
}

func (*revisionStreamStub) EnsureGroup(context.Context) error { return nil }
func (*revisionStreamStub) Add(context.Context, taskqueue.RevisionStreamEvent) (string, error) {
	return "", errors.New("not used")
}
func (s *revisionStreamStub) Read(context.Context, int64, time.Duration) ([]taskqueue.RevisionStreamMessage, error) {
	return nil, nil
}
func (s *revisionStreamStub) Reclaim(context.Context, time.Duration, int64) ([]taskqueue.RevisionStreamMessage, error) {
	messages := s.reclaimed
	s.reclaimed = nil
	return messages, nil
}
func (s *revisionStreamStub) Ack(_ context.Context, messageIDs ...string) error {
	s.acked = append(s.acked, messageIDs...)
	return nil
}
func (s *revisionStreamStub) DeadLetter(context.Context, taskqueue.RevisionStreamEvent, string, string) (string, error) {
	s.deadLettered = true
	return "1710000000001-0", nil
}
func (*revisionStreamStub) Close() error { return nil }
