package auth

import (
	"context"
	"testing"
	"time"
)

func TestManagerCreatesAndClearsMemorySession(t *testing.T) {
	manager := NewManager([]Credential{{
		User:     User{UserID: "user_1", Username: "admin", Role: RoleAdmin},
		Password: "secret",
	}}, time.Hour)

	user, ok := manager.Authenticate("admin", "secret")
	if !ok {
		t.Fatal("expected authentication to succeed")
	}
	token, expiresAt, err := manager.CreateSession(user)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if token == "" || !expiresAt.After(time.Now().UTC()) {
		t.Fatalf("unexpected session token or expiry: token=%q expires=%s", token, expiresAt)
	}

	got, ok := manager.UserForToken(token)
	if !ok || got.UserID != user.UserID || got.Role != RoleAdmin {
		t.Fatalf("unexpected user for token: %#v ok=%v", got, ok)
	}

	manager.ClearSession(token)
	if _, ok := manager.UserForToken(token); ok {
		t.Fatal("expected session to be cleared")
	}
}

func TestRoleCapabilitiesAndDefaultCredentialCompatibility(t *testing.T) {
	t.Setenv("SEE_AGENT_ADMIN_USERNAME", "legacy-system")
	t.Setenv("SEE_AGENT_ADMIN_PASSWORD", "legacy-password")
	t.Setenv("SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME", "knowledge")
	t.Setenv("SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD", "knowledge-password")
	credentials := DefaultCredentialsFromEnv()
	if len(credentials) != 3 {
		t.Fatalf("credential count=%d", len(credentials))
	}
	if credentials[0].User.UserID != "user_admin" || credentials[0].User.Role != RoleSystemAdmin || credentials[0].User.Username != "legacy-system" {
		t.Fatalf("system credential=%#v", credentials[0])
	}
	if credentials[1].User.Role != RoleKnowledgeAdmin || credentials[1].User.Username != "knowledge" {
		t.Fatalf("knowledge credential=%#v", credentials[1])
	}
	if !IsKnowledgeAdminRole(RoleKnowledgeAdmin) || !IsKnowledgeAdminRole(RoleSystemAdmin) || !IsKnowledgeAdminRole(RoleLegacyAdmin) {
		t.Fatal("expected knowledge-management roles to be accepted")
	}
	if IsSystemAdminRole(RoleKnowledgeAdmin) || !IsSystemAdminRole(RoleLegacyAdmin) || !IsKnownRole(RoleUser) || IsKnownRole("forged") {
		t.Fatal("unexpected role capability result")
	}

	t.Setenv("SEE_AGENT_SYSTEM_ADMIN_USERNAME", "preferred-system")
	if got := DefaultCredentialsFromEnv()[0].User.Username; got != "preferred-system" {
		t.Fatalf("new system-admin environment variable did not win: %q", got)
	}
}

func TestMemorySessionStoreExpiresRecord(t *testing.T) {
	store := NewMemorySessionStore()
	token := "expired"
	err := store.Save(context.Background(), token, SessionRecord{
		User:      User{UserID: "user_1", Username: "admin", Role: RoleAdmin},
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("save session: %v", err)
	}

	if _, ok, err := store.Find(context.Background(), token); err != nil || ok {
		t.Fatalf("expected expired session to miss, ok=%v err=%v", ok, err)
	}
}
