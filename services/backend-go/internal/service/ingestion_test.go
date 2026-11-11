package service_test

import (
	"context"
	"errors"
	"testing"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
	"see-agent/services/backend-go/internal/service"
	"see-agent/services/backend-go/internal/taskqueue"
)

type recordingUploadStore struct {
	fileID      string
	filename    string
	contentType string
	content     string
	err         error
}

func (s *recordingUploadStore) Put(_ context.Context, fileID, filename, contentType string, content []byte) (string, error) {
	s.fileID = fileID
	s.filename = filename
	s.contentType = contentType
	s.content = string(content)
	if s.err != nil {
		return "", s.err
	}
	return "s3://knowledge-docs/kb/" + fileID + "/" + filename, nil
}

func TestCreateFileUsesUploadStoreBeforeCreatingMetadata(t *testing.T) {
	store := repository.NewMemoryStore()
	uploads := &recordingUploadStore{}
	svc := service.NewWithUploadStore(store, nil, uploads, false)

	file, task, err := svc.CreateFile("policy.txt", "text/plain", []byte("policy body"), false)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}

	if uploads.fileID == "" || uploads.fileID != file.FileID {
		t.Fatalf("expected reserved file id to be used by upload store, upload=%s file=%s", uploads.fileID, file.FileID)
	}
	if uploads.filename != "policy.txt" || uploads.contentType != "text/plain" || uploads.content != "policy body" {
		t.Fatalf("unexpected upload call: %#v", uploads)
	}
	if file.ObjectURI != "s3://knowledge-docs/kb/"+file.FileID+"/policy.txt" {
		t.Fatalf("unexpected object uri: %s", file.ObjectURI)
	}
	if task.FileID != file.FileID || task.Status != "queued" {
		t.Fatalf("unexpected task: %#v", task)
	}
}

func TestCreateFileDoesNotCreateMetadataWhenUploadFails(t *testing.T) {
	store := repository.NewMemoryStore()
	uploads := &recordingUploadStore{err: errors.New("object store down")}
	svc := service.NewWithUploadStore(store, nil, uploads, false)

	_, _, err := svc.CreateFile("policy.txt", "text/plain", []byte("policy body"), false)
	if err == nil {
		t.Fatal("expected upload failure")
	}

	files := store.ListFiles("", "")
	for _, file := range files {
		if file.Filename == "policy.txt" {
			t.Fatalf("did not expect file metadata after upload failure: %#v", file)
		}
	}
}

func TestCreateFileMarksTaskFailedWhenQueueUnavailable(t *testing.T) {
	store := repository.NewMemoryStore()
	uploads := &recordingUploadStore{}
	svc := service.NewWithDependencies(store, nil, uploads, failingIngestionQueue{err: errors.New("redis down")}, false)
	defer svc.Close()

	file, _, err := svc.CreateFile("policy.txt", "text/plain", []byte("policy body"), false)
	if !errors.Is(err, service.ErrIngestionQueueUnavailable) {
		t.Fatalf("expected ErrIngestionQueueUnavailable, got %v", err)
	}

	_, latestTask, ok := store.GetFile(file.FileID)
	if !ok {
		t.Fatalf("expected file metadata to exist after enqueue failure")
	}
	if latestTask.Status != "failed" || latestTask.Error == nil || latestTask.Error.Code != "queue_unavailable" {
		t.Fatalf("expected failed queue task, got %#v", latestTask)
	}
}

type noOpRuntimeClient struct{}

func (noOpRuntimeClient) RunIngestion(context.Context, domain.KnowledgeFile, domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	return domain.IngestionTaskResult{}, nil
}

func (noOpRuntimeClient) RunChat(context.Context, domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	return domain.ChatRuntimeResponse{}, nil
}

func (noOpRuntimeClient) StreamChat(context.Context, domain.ChatRuntimeRequest, func(domain.ChatRuntimeStreamEvent) error) error {
	return nil
}

type failingIngestionQueue struct {
	err error
}

func (q failingIngestionQueue) Enqueue(context.Context, taskqueue.IngestionJob) error {
	return q.err
}

func (failingIngestionQueue) Dequeue(ctx context.Context) (taskqueue.IngestionJob, error) {
	<-ctx.Done()
	return taskqueue.IngestionJob{}, ctx.Err()
}

func (failingIngestionQueue) Close() error {
	return nil
}
