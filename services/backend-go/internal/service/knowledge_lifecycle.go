package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

var (
	ErrKnowledgeLifecycleUnavailable = errors.New("knowledge lifecycle requires postgres")
	ErrInvalidKnowledgeUpload        = errors.New("invalid knowledge upload")
)

const (
	parseVersionK4 = "parse_v1"
	indexVersionK4 = "index_v1"
)

type KnowledgeDocumentUploadInput struct {
	ActorID        string
	IdempotencyKey string
	Filename       string
	MIMEType       string
	Content        []byte
	DisplayName    string
	EffectiveAt    string
}

type KnowledgeRevisionUploadInput struct {
	ActorID        string
	IdempotencyKey string
	DocumentID     string
	Filename       string
	MIMEType       string
	Content        []byte
	EffectiveAt    string
}

func (s *Service) KnowledgeLifecycleEnabled() bool {
	_, ok := s.store.(repository.KnowledgeLifecycleStore)
	return ok
}

func (s *Service) CreateKnowledgeDocument(ctx context.Context, input KnowledgeDocumentUploadInput) (domain.KnowledgeRevisionUpload, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	prepared, err := prepareKnowledgeUpload(input.Filename, input.MIMEType, input.Content, input.EffectiveAt)
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = input.Filename
	}
	if len(displayName) > 240 {
		return domain.KnowledgeRevisionUpload{}, fmt.Errorf("%w: display_name exceeds 240 characters", ErrInvalidKnowledgeUpload)
	}
	key := lifecycleMutationKey(input.ActorID, repository.KnowledgeOperationCreateDocument(), input.IdempotencyKey, prepared.checksum, displayName, effectiveAtValue(prepared.effectiveAt))
	if replay, found, err := replayKnowledgeUpload(ctx, store, key); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	} else if found {
		return replay, nil
	}
	documentID := lifecycleID("doc", key)
	revisionID := lifecycleID("rev", key)
	objectURI, err := s.putKnowledgeObject(ctx, revisionID, input.Filename, prepared.mimeType, input.Content)
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	return store.CreateKnowledgeDocumentRevision(ctx,
		domain.KnowledgeDocument{DocumentID: documentID, DisplayName: displayName, OwnerID: strings.TrimSpace(input.ActorID)},
		prepared.revision(documentID, revisionID, input.Filename, objectURI, ""),
		key,
	)
}

func (s *Service) CreateKnowledgeRevision(ctx context.Context, input KnowledgeRevisionUploadInput) (domain.KnowledgeRevisionUpload, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	prepared, err := prepareKnowledgeUpload(input.Filename, input.MIMEType, input.Content, input.EffectiveAt)
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	documentID := strings.TrimSpace(input.DocumentID)
	if documentID == "" {
		return domain.KnowledgeRevisionUpload{}, fmt.Errorf("%w: document_id is required", ErrInvalidKnowledgeUpload)
	}
	key := lifecycleMutationKey(input.ActorID, repository.KnowledgeOperationCreateRevision(), input.IdempotencyKey, documentID, prepared.checksum, effectiveAtValue(prepared.effectiveAt))
	if replay, found, err := replayKnowledgeUpload(ctx, store, key); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	} else if found {
		return replay, nil
	}
	revisionID := lifecycleID("rev", key)
	objectURI, err := s.putKnowledgeObject(ctx, revisionID, input.Filename, prepared.mimeType, input.Content)
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	return store.CreateKnowledgeReplacementRevision(ctx, prepared.revision(documentID, revisionID, input.Filename, objectURI, ""), key)
}

func (s *Service) ListKnowledgeDocuments(ctx context.Context, status, cursor string, limit int) (domain.KnowledgeDocumentPage, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeDocumentPage{}, err
	}
	return store.ListKnowledgeDocuments(ctx, status, cursor, limit)
}

func (s *Service) GetKnowledgeDocument(ctx context.Context, documentID string) (domain.KnowledgeDocumentDetail, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeDocumentDetail{}, err
	}
	return store.GetKnowledgeDocument(ctx, documentID)
}

func (s *Service) GetKnowledgeRetirementReadiness(ctx context.Context, documentID string) (domain.KnowledgeRetirementReadiness, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeRetirementReadiness{}, err
	}
	return store.GetKnowledgeRetirementReadiness(ctx, documentID)
}

func (s *Service) PrepareKnowledgeRevisionPublish(ctx context.Context, actorID, revisionID, idempotencyKey string) (domain.KnowledgeLifecycleMutation, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	key := lifecycleMutationKey(actorID, repository.KnowledgeOperationPreparePublish(), idempotencyKey, revisionID)
	if replay, found, err := replayKnowledgeMutation(ctx, store, key); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		return replay, nil
	}
	return store.PrepareKnowledgeRevisionPublish(ctx, revisionID, key)
}

func (s *Service) ActivateKnowledgeSnapshot(ctx context.Context, actorID, snapshotID, idempotencyKey string, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	key := lifecycleMutationKey(actorID, repository.KnowledgeOperationActivateSnapshot(), idempotencyKey, snapshotID, acknowledgementFlag(acknowledgeUnvalidated))
	if replay, found, err := replayKnowledgeMutation(ctx, store, key); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		return replay, nil
	}
	return store.ActivateKnowledgeSnapshot(ctx, snapshotID, key, acknowledgeUnvalidated)
}

func (s *Service) PublishKnowledgeRevision(ctx context.Context, actorID, revisionID, idempotencyKey string, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	key := lifecycleMutationKey(actorID, repository.KnowledgeOperationPublishRevision(), idempotencyKey, revisionID, acknowledgementFlag(acknowledgeUnvalidated))
	if replay, found, err := replayKnowledgeMutation(ctx, store, key); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		return replay, nil
	}
	return store.PublishKnowledgeRevisionLifecycle(ctx, revisionID, key, acknowledgeUnvalidated)
}

func acknowledgementFlag(acknowledgeUnvalidated bool) string {
	if acknowledgeUnvalidated {
		return "acknowledge_unvalidated"
	}
	return "no_acknowledgement"
}

func (s *Service) DisableKnowledgeDocument(ctx context.Context, actorID, documentID, idempotencyKey string) (domain.KnowledgeLifecycleMutation, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	key := lifecycleMutationKey(actorID, repository.KnowledgeOperationDisableDocument(), idempotencyKey, documentID)
	if replay, found, err := replayKnowledgeMutation(ctx, store, key); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		return replay, nil
	}
	return store.DisableKnowledgeDocumentAfterReadiness(ctx, documentID, key)
}

func (s *Service) DeleteKnowledgeDocument(ctx context.Context, actorID, documentID, idempotencyKey string) (domain.KnowledgeLifecycleMutation, error) {
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	key := lifecycleMutationKey(actorID, repository.KnowledgeOperationDeleteDocument(), idempotencyKey, documentID)
	if replay, found, err := replayKnowledgeMutation(ctx, store, key); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		return replay, nil
	}
	return store.DeleteKnowledgeDocumentAfterReadiness(ctx, documentID, key)
}

// CreateLegacyFileAs maps an old upload endpoint to the K4 authoritative
// document/revision write path whenever Postgres is configured. Memory smoke
// keeps its existing List-based behavior through CreateFile below.
func (s *Service) CreateLegacyFileAs(ctx context.Context, actorID, filename, mimeType string, content []byte, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary, error) {
	if !s.KnowledgeLifecycleEnabled() {
		return s.createFileWithDispatch(filename, mimeType, content, forceReindex)
	}
	store, err := s.knowledgeLifecycleStore()
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, err
	}
	prepared, err := prepareKnowledgeUpload(filename, mimeType, content, "")
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, err
	}
	fileID := s.store.ReserveFileID()
	if strings.TrimSpace(fileID) == "" {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, fmt.Errorf("reserve legacy file id")
	}
	key := lifecycleMutationKey(actorID, repository.KnowledgeOperationCreateDocument(), "legacy-"+fileID, fileID, prepared.checksum, filename)
	documentID := lifecycleID("doc", key)
	revisionID := lifecycleID("rev", key)
	objectURI, err := s.putKnowledgeObject(ctx, revisionID, filename, prepared.mimeType, content)
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, err
	}
	if _, err := store.CreateKnowledgeDocumentRevision(ctx,
		domain.KnowledgeDocument{DocumentID: documentID, DisplayName: filename, OwnerID: strings.TrimSpace(actorID)},
		prepared.revision(documentID, revisionID, filename, objectURI, fileID), key,
	); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, err
	}
	file, task, ok := s.store.GetFile(fileID)
	if !ok {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, fmt.Errorf("legacy knowledge facade missing after lifecycle create")
	}
	return file, task, nil
}

func (s *Service) knowledgeLifecycleStore() (repository.KnowledgeLifecycleStore, error) {
	store, ok := s.store.(repository.KnowledgeLifecycleStore)
	if !ok {
		return nil, ErrKnowledgeLifecycleUnavailable
	}
	return store, nil
}

func (s *Service) putKnowledgeObject(parent context.Context, revisionID, filename, mimeType string, content []byte) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	uri, err := s.uploadStore.Put(ctx, revisionID, filename, mimeType, content)
	if err != nil {
		return "", fmt.Errorf("persist revision object: %w", err)
	}
	return uri, nil
}

type preparedKnowledgeUpload struct {
	mimeType     string
	sourceFormat string
	checksum     string
	effectiveAt  *string
}

func prepareKnowledgeUpload(filename, mimeType string, content []byte, effectiveAt string) (preparedKnowledgeUpload, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || len(content) == 0 {
		return preparedKnowledgeUpload{}, fmt.Errorf("%w: file is required and cannot be empty", ErrInvalidKnowledgeUpload)
	}
	format, detectedMIME := knowledgeSourceFormat(filename)
	if format == "" {
		return preparedKnowledgeUpload{}, fmt.Errorf("%w: unsupported file format", ErrInvalidKnowledgeUpload)
	}
	mimeType = strings.TrimSpace(strings.Split(mimeType, ";")[0])
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = detectedMIME
	}
	prepared := preparedKnowledgeUpload{mimeType: mimeType, sourceFormat: format, checksum: "sha256:" + sha256Hex(content)}
	if value := strings.TrimSpace(effectiveAt); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return preparedKnowledgeUpload{}, fmt.Errorf("%w: effective_at must be RFC3339", ErrInvalidKnowledgeUpload)
		}
		normalized := parsed.UTC().Format(time.RFC3339)
		prepared.effectiveAt = &normalized
	}
	return prepared, nil
}

func (p preparedKnowledgeUpload) revision(documentID, revisionID, filename, objectURI, legacyFileID string) domain.KnowledgeRevision {
	return domain.KnowledgeRevision{
		RevisionID: revisionID, DocumentID: documentID, LegacyFileID: legacyFileID, Filename: strings.TrimSpace(filename),
		MIMEType: p.mimeType, SourceFormat: p.sourceFormat, ObjectURI: objectURI, ChecksumSHA256: p.checksum,
		ParseVersion: parseVersionK4, IndexVersion: indexVersionK4, EffectiveAt: p.effectiveAt,
	}
}

func knowledgeSourceFormat(filename string) (string, string) {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".txt":
		return "txt", "text/plain"
	case ".md", ".markdown":
		return "md", "text/markdown"
	case ".json":
		return "json", "application/json"
	case ".pdf":
		return "pdf", "application/pdf"
	case ".docx":
		return "docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	default:
		return "", ""
	}
}

func lifecycleMutationKey(actorID, operation, idempotencyKey string, parts ...string) domain.KnowledgeMutationKey {
	builder := strings.Builder{}
	builder.WriteString(operation)
	for _, part := range parts {
		builder.WriteByte('\n')
		builder.WriteString(strings.TrimSpace(part))
	}
	return domain.KnowledgeMutationKey{
		ActorID: strings.TrimSpace(actorID), Operation: operation, Key: strings.TrimSpace(idempotencyKey),
		RequestHash: "sha256:" + sha256Hex([]byte(builder.String())),
	}
}

func lifecycleID(prefix string, key domain.KnowledgeMutationKey) string {
	digest := sha256Hex([]byte(key.ActorID + "\n" + key.Operation + "\n" + key.Key))
	return prefix + "_" + digest[:20]
}

func sha256Hex(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func effectiveAtValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func replayKnowledgeUpload(ctx context.Context, store repository.KnowledgeLifecycleStore, key domain.KnowledgeMutationKey) (domain.KnowledgeRevisionUpload, bool, error) {
	raw, found, err := store.GetKnowledgeMutation(ctx, key)
	if err != nil || !found {
		return domain.KnowledgeRevisionUpload{}, found, err
	}
	var replay domain.KnowledgeRevisionUpload
	if err := json.Unmarshal(raw, &replay); err != nil {
		return domain.KnowledgeRevisionUpload{}, false, fmt.Errorf("decode idempotent knowledge upload: %w", err)
	}
	return replay, true, nil
}

func replayKnowledgeMutation(ctx context.Context, store repository.KnowledgeLifecycleStore, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, bool, error) {
	raw, found, err := store.GetKnowledgeMutation(ctx, key)
	if err != nil || !found {
		return domain.KnowledgeLifecycleMutation{}, found, err
	}
	var replay domain.KnowledgeLifecycleMutation
	if err := json.Unmarshal(raw, &replay); err != nil {
		return domain.KnowledgeLifecycleMutation{}, false, fmt.Errorf("decode idempotent knowledge mutation: %w", err)
	}
	return replay, true, nil
}
