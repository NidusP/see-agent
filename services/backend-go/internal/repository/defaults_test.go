package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestNewMemoryStoreUsesFallbackModelForBlankDefault(t *testing.T) {
	for _, value := range []string{"", "   "} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("LOCAL_LLM_DEFAULT_MODEL_ID", value)

			if got := NewMemoryStore().ActiveConfig().ModelID; got != fallbackLocalLLMModelID {
				t.Fatalf("default model_id = %q, want %q", got, fallbackLocalLLMModelID)
			}
		})
	}
}

func TestNewMemoryStoreUsesExplicitDefaultAgentModelID(t *testing.T) {
	t.Setenv("LOCAL_LLM_DEFAULT_MODEL_ID", "  local-model:stable  ")

	if got := NewMemoryStore().ActiveConfig().ModelID; got != "local-model:stable" {
		t.Fatalf("default model_id = %q, want %q", got, "local-model:stable")
	}
}

func TestPostgresSeedUsesDefaultAgentModelIDWithoutOverwritingExistingConfigs(t *testing.T) {
	t.Setenv("LOCAL_LLM_DEFAULT_MODEL_ID", "local-model:stable")
	db, recorder := newSeedRecordingDB(t)
	store := &PostgresStore{db: db}

	if err := store.seedDefaults(context.Background()); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}

	call, ok := recorder.agentConfigSeedCall()
	if !ok {
		t.Fatal("agent config seed query was not recorded")
	}
	if len(call.args) != 2 || call.args[0].Value != "local-model:stable" {
		t.Fatalf("agent config seed args = %#v, want model_id local-model:stable", call.args)
	}
	if !strings.Contains(call.query, "WHERE NOT EXISTS (SELECT 1 FROM agent_configs)") {
		t.Fatalf("agent config seed must only run for an empty table: %s", call.query)
	}
	if strings.Contains(strings.ToUpper(call.query), " DO UPDATE") {
		t.Fatalf("agent config seed must not update existing configs: %s", call.query)
	}
}

func TestNewConversationSnapshotsActiveConfigModelID(t *testing.T) {
	t.Setenv("LOCAL_LLM_DEFAULT_MODEL_ID", "bootstrap-model:latest")
	store := NewMemoryStore()
	active := store.CreateConfig("admin-selected:latest", "prompt_v2", "concise", "", 0.3, 1, 2048)

	conversation := store.CreateConversation("Model snapshot")
	if conversation.ConfigVersion != active.ConfigVersion {
		t.Fatalf("conversation config_version = %q, want %q", conversation.ConfigVersion, active.ConfigVersion)
	}
	if conversation.ModelID != active.ModelID {
		t.Fatalf("conversation model_id = %q, want active model_id %q", conversation.ModelID, active.ModelID)
	}
}

func TestMemoryStoreGetConfigReturnsExactVersion(t *testing.T) {
	store := NewMemoryStore()
	first := store.ActiveConfig()
	second := store.CreateConfig("second-model:latest", "prompt_v2", "concise", "", 0.7, 1, 2048)

	config, ok := store.GetConfig(first.ConfigVersion)
	if !ok {
		t.Fatalf("expected config %q to be found", first.ConfigVersion)
	}
	if config != first {
		t.Fatalf("config = %#v, want %#v", config, first)
	}
	if _, ok := store.GetConfig(second.ConfigVersion + "-missing"); ok {
		t.Fatal("expected unknown config version not to be found")
	}
}

func TestPostgresStoreGetConfigQueriesExactVersion(t *testing.T) {
	db, recorder := newSeedRecordingDB(t)
	store := &PostgresStore{db: db}

	if _, ok := store.GetConfig("cfg_123"); ok {
		t.Fatal("expected recording database to return no config")
	}
	call, ok := recorder.configByVersionQuery()
	if !ok {
		t.Fatal("config version query was not recorded")
	}
	if !strings.Contains(call.query, "FROM agent_configs WHERE config_version = $1") {
		t.Fatalf("config query must select by exact version: %s", call.query)
	}
	if len(call.args) != 1 || call.args[0].Value != "cfg_123" {
		t.Fatalf("config query args = %#v, want config_version cfg_123", call.args)
	}
}

type recordedExec struct {
	query string
	args  []driver.NamedValue
}

type seedRecorder struct {
	mu      sync.Mutex
	calls   []recordedExec
	queries []recordedExec
}

func (r *seedRecorder) record(query string, args []driver.NamedValue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedExec{query: strings.Join(strings.Fields(query), " "), args: append([]driver.NamedValue(nil), args...)})
}

func (r *seedRecorder) recordQuery(query string, args []driver.NamedValue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, recordedExec{query: strings.Join(strings.Fields(query), " "), args: append([]driver.NamedValue(nil), args...)})
}

func (r *seedRecorder) agentConfigSeedCall() (recordedExec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if strings.Contains(call.query, "INSERT INTO agent_configs") {
			return call, true
		}
	}
	return recordedExec{}, false
}

func (r *seedRecorder) configByVersionQuery() (recordedExec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.queries {
		if strings.Contains(call.query, "FROM agent_configs WHERE config_version = $1") {
			return call, true
		}
	}
	return recordedExec{}, false
}

var (
	seedRecordingDriverOnce sync.Once
	seedRecordingDriverMu   sync.Mutex
	seedRecordingCurrent    *seedRecorder
)

func newSeedRecordingDB(t *testing.T) (*sql.DB, *seedRecorder) {
	t.Helper()
	seedRecordingDriverOnce.Do(func() {
		sql.Register("repository-seed-recording", seedRecordingDriver{})
	})

	recorder := &seedRecorder{}
	seedRecordingDriverMu.Lock()
	seedRecordingCurrent = recorder
	seedRecordingDriverMu.Unlock()

	db, err := sql.Open("repository-seed-recording", "")
	if err != nil {
		t.Fatalf("open recording database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		seedRecordingDriverMu.Lock()
		seedRecordingCurrent = nil
		seedRecordingDriverMu.Unlock()
	})
	return db, recorder
}

type seedRecordingDriver struct{}

func (seedRecordingDriver) Open(string) (driver.Conn, error) {
	seedRecordingDriverMu.Lock()
	recorder := seedRecordingCurrent
	seedRecordingDriverMu.Unlock()
	if recorder == nil {
		return nil, errors.New("seed recorder is not configured")
	}
	return seedRecordingConn{recorder: recorder}, nil
}

type seedRecordingConn struct {
	recorder *seedRecorder
}

func (c seedRecordingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported")
}

func (seedRecordingConn) Close() error {
	return nil
}

func (seedRecordingConn) Begin() (driver.Tx, error) {
	return seedRecordingTx{}, nil
}

func (c seedRecordingConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.recorder.record(query, args)
	return driver.RowsAffected(1), nil
}

func (c seedRecordingConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.recorder.recordQuery(query, args)
	return seedRecordingRows{}, nil
}

type seedRecordingRows struct{}

func (seedRecordingRows) Columns() []string {
	return []string{"config_version"}
}

func (seedRecordingRows) Close() error {
	return nil
}

func (seedRecordingRows) Next([]driver.Value) error {
	return io.EOF
}

type seedRecordingTx struct{}

func (seedRecordingTx) Commit() error {
	return nil
}

func (seedRecordingTx) Rollback() error {
	return nil
}

var _ driver.ExecerContext = seedRecordingConn{}
var _ driver.QueryerContext = seedRecordingConn{}
