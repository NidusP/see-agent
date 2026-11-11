package taskqueue

import "testing"

func TestDecodeRevisionStreamEvent(t *testing.T) {
	event, err := decodeRevisionStreamEvent(map[string]any{
		"outbox_id":       "delivery_001",
		"delivery_id":     "delivery_001",
		"document_id":     "doc_001",
		"revision_id":     "rev_001",
		"checksum_sha256": "sha256:body",
		"attempt":         "2",
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if event.Attempt != 2 || event.RevisionID != "rev_001" {
		t.Fatalf("decoded event = %#v", event)
	}
}

func TestDecodeRevisionStreamEventRejectsIdentityMismatch(t *testing.T) {
	_, err := decodeRevisionStreamEvent(map[string]any{
		"outbox_id":       "delivery_001",
		"delivery_id":     "delivery_002",
		"document_id":     "doc_001",
		"revision_id":     "rev_001",
		"checksum_sha256": "sha256:body",
		"attempt":         "0",
	})
	if err == nil {
		t.Fatal("expected identity mismatch to be rejected")
	}
}
