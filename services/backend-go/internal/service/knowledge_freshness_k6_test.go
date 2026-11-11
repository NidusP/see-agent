package service

import (
	"testing"

	"see-agent/services/backend-go/internal/domain"
)

func TestK6RuntimeStyleResponseRequiresRevalidatedFreshness(t *testing.T) {
	scope := domain.RuntimeKnowledgeScope{SnapshotID: "snap_k6", ManifestHash: "sha256:k6", RevisionIDs: []string{"rev_k6"}, ResolvedAt: "2026-07-15T00:00:00Z"}
	request := domain.ChatRuntimeRequest{RequestID: "req_k6", ConversationID: "conv_k6", MessageID: "reval_k6", KnowledgeSnapshotID: scope.SnapshotID, KnowledgeScope: &scope, AnswerMode: "revalidate"}
	response := domain.ChatRuntimeResponse{
		RequestID: "req_k6", ConversationID: "conv_k6", MessageID: "reval_k6", KnowledgeSnapshotID: scope.SnapshotID,
		KnowledgeSnapshotManifestHash: scope.ManifestHash, FreshnessStatus: "revalidated", RevalidationOutcome: "needs_update",
		Citations: []domain.RuntimeCitation{{DocumentID: "doc_k6", RevisionID: "rev_k6", ChunkContentSHA256: "sha256:chunk", KnowledgeSnapshotID: scope.SnapshotID}},
	}
	if err := validateRevalidationRuntimeResponse(request, response); err != nil {
		t.Fatalf("Runtime-style K6 response should validate: %v", err)
	}
	response.FreshnessStatus = "current"
	if err := validateRevalidationRuntimeResponse(request, response); err == nil {
		t.Fatal("normal-answer freshness must not be accepted for K6")
	}
}
