package objectstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalStorePutWritesUploadFile(t *testing.T) {
	root := t.TempDir()
	store := LocalStore{Root: root}

	uri, err := store.Put(context.Background(), "file_123", "../policy.txt", "text/plain", []byte("policy body"))
	if err != nil {
		t.Fatalf("put local object: %v", err)
	}

	if !strings.HasPrefix(uri, root) {
		t.Fatalf("expected object path under %s, got %s", root, uri)
	}
	if filepath.Base(uri) != "policy.txt" {
		t.Fatalf("expected sanitized filename policy.txt, got %s", filepath.Base(uri))
	}
	body, err := os.ReadFile(uri)
	if err != nil {
		t.Fatalf("read local object: %v", err)
	}
	if string(body) != "policy body" {
		t.Fatalf("unexpected local object body: %s", string(body))
	}
}

func TestS3StorePutCreatesBucketAndUploadsObject(t *testing.T) {
	var seen []string
	var uploadedBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") == "" {
			t.Fatalf("expected Authorization header for %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Amz-Date") != "20260703T120000Z" {
			t.Fatalf("unexpected X-Amz-Date: %s", r.Header.Get("X-Amz-Date"))
		}

		switch r.Method + " " + r.URL.Path {
		case "HEAD /knowledge-docs":
			w.WriteHeader(http.StatusNotFound)
		case "PUT /knowledge-docs":
			w.WriteHeader(http.StatusOK)
		case "PUT /knowledge-docs/kb/file_123/policy.txt":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upload body: %v", err)
			}
			uploadedBody = string(body)
			if r.Header.Get("Content-Type") != "text/plain" {
				t.Fatalf("expected text/plain content type, got %s", r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	store := NewS3Store(S3Config{
		Endpoint:  server.URL,
		Bucket:    "knowledge-docs",
		Region:    "us-east-1",
		AccessKey: "see_agent",
		SecretKey: "see_agent_dev",
	}, server.Client())
	store.now = func() time.Time {
		return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	}

	uri, err := store.Put(context.Background(), "file_123", "policy.txt", "text/plain", []byte("policy body"))
	if err != nil {
		t.Fatalf("put s3 object: %v", err)
	}

	if uri != "s3://knowledge-docs/kb/file_123/policy.txt" {
		t.Fatalf("unexpected object uri: %s", uri)
	}
	if uploadedBody != "policy body" {
		t.Fatalf("unexpected uploaded body: %s", uploadedBody)
	}
	expected := []string{
		"HEAD /knowledge-docs",
		"PUT /knowledge-docs",
		"PUT /knowledge-docs/kb/file_123/policy.txt",
	}
	if strings.Join(seen, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected request sequence: %#v", seen)
	}
}
