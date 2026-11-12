package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestPostgresConversationOwnershipMigrationAndIsolation(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K9_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K9_TEST_DATABASE_URL to run the isolated Postgres ownership smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k9_owner_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK3Identifier(schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK3Identifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()

	databaseURL := k3DatabaseURLWithSearchPath(t, baseURL, schema)
	legacyDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		legacyDB.Close()
		t.Fatalf("create migration ledger: %v", err)
	}
	for _, migration := range postgresMigrations() {
		if migration.version == "010" {
			break
		}
		for _, statement := range migration.statements {
			if _, err := legacyDB.ExecContext(ctx, statement); err != nil {
				legacyDB.Close()
				t.Fatalf("apply legacy migration %s: %v", migration.version, err)
			}
		}
		if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, NOW())`, migration.version, migration.name); err != nil {
			legacyDB.Close()
			t.Fatalf("record legacy migration %s: %v", migration.version, err)
		}
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO conversations (
			conversation_id, title, created_at, updated_at, config_version,
			retrieval_config_version, knowledge_snapshot_id, model_id
		) VALUES ('conv_legacy', 'Legacy private', NOW(), NOW(), 'cfg_legacy', 'rcfg_legacy', 'snap_legacy', 'model_legacy')`); err != nil {
		legacyDB.Close()
		t.Fatalf("insert legacy conversation: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := NewPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("bootstrap ownership store: %v", err)
	}
	defer store.Close()
	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil || strings.Join(versions, ",") != "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024,025,026" {
		t.Fatalf("migration versions = %#v err=%v", versions, err)
	}
	transitionConversation := store.CreateOwnedConversation("user_member", "attempt transition")
	attempt, err := store.CreateChatExecutionAttempt(ctx, domain.ChatExecutionAttempt{
		AttemptID: "attempt_postgres_transition", RequestID: "request_postgres_transition", ConversationID: transitionConversation.ConversationID,
		AssistantMessageID: "msg_postgres_transition", State: domain.ChatExecutionAttemptAccepted,
		ConfigVersion: transitionConversation.ConfigVersion, RetrievalConfigVersion: transitionConversation.RetrievalConfigVersion,
	})
	if err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	running, err := store.TransitionChatExecutionAttempt(ctx, attempt.AttemptID, domain.ChatExecutionAttemptRunning, "", "", false)
	if err != nil || running.StartedAt == "" || running.TerminalAt != "" {
		t.Fatalf("accepted -> running = %#v err=%v", running, err)
	}
	failed, err := store.TransitionChatExecutionAttempt(ctx, attempt.AttemptID, domain.ChatExecutionAttemptFailed, "dependency_unavailable", "runtime_call", true)
	if err != nil || failed.TerminalAt == "" || failed.ErrorCode != "dependency_unavailable" {
		t.Fatalf("running -> failed = %#v err=%v", failed, err)
	}
	legacy, _, found := store.GetOwnedConversation("user_admin", "conv_legacy")
	if !found || legacy.OwnerID != "user_admin" || legacy.TitleSource != ConversationTitleLegacy {
		t.Fatalf("legacy ownership = %#v found=%v", legacy, found)
	}
	if _, _, found := store.GetOwnedConversation("user_member", "conv_legacy"); found {
		t.Fatal("legacy conversation leaked to user_member")
	}
	if _, _, found := store.GetOwnedConversation("", "conv_legacy"); found {
		t.Fatal("blank owner must not receive implicit admin access")
	}

	memberConversation := store.CreateOwnedConversation("user_member", "Member private")
	if memberConversation.ConversationID == "" || memberConversation.OwnerID != "user_member" {
		t.Fatalf("member conversation = %#v", memberConversation)
	}
	memberItems := store.ListOwnedConversations("user_member")
	memberFound := false
	for _, item := range memberItems {
		memberFound = memberFound || item.ConversationID == memberConversation.ConversationID
	}
	if !memberFound {
		t.Fatalf("member list = %#v", memberItems)
	}
	adminItems := store.ListOwnedConversations("user_admin")
	if len(adminItems) != 1 || adminItems[0].ConversationID != "conv_legacy" {
		t.Fatalf("admin list = %#v", adminItems)
	}
	if conversation := store.CreateOwnedConversation("", "Invalid owner"); conversation.ConversationID != "" {
		t.Fatalf("blank owner created conversation: %#v", conversation)
	}
	titled := store.CreateOwnedConversation("user_member", "")
	if titled.Title != DefaultConversationTitle || titled.TitleSource != ConversationTitleSystemDefault {
		t.Fatalf("default title conversation = %#v", titled)
	}
	if _, _, _, ok := store.AppendRuntimeExchange(titled.ConversationID, "  PostgreSQL  首问标题  ", domain.ChatRuntimeResponse{
		MessageID:        "msg_postgres_title_001",
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "answer"},
		FinishReason:     "stop",
		Intent:           "social_greeting",
	}); !ok {
		t.Fatal("append title exchange")
	}
	titled, _, found = store.GetOwnedConversation("user_member", titled.ConversationID)
	if !found || titled.Title != "PostgreSQL 首问标题" || titled.TitleSource != ConversationTitleFirstUserMessage {
		t.Fatalf("automatic title = %#v found=%v", titled, found)
	}
	if renamed, ok := store.RenameOwnedConversation("user_member", titled.ConversationID, "Postgres 手动标题"); !ok || renamed.TitleSource != ConversationTitleUserEdited {
		t.Fatalf("rename title = %#v ok=%v", renamed, ok)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO conversations (
			conversation_id, title, created_at, updated_at, config_version,
			retrieval_config_version, knowledge_snapshot_id, model_id
		) VALUES ('conv_without_owner', 'Invalid', NOW(), NOW(), 'cfg', 'rcfg', 'snap', 'model')`); err == nil {
		t.Fatal("owner_id NOT NULL constraint was not enforced")
	}
}
