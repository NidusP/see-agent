package repository

import (
	"context"
	"database/sql"
	"fmt"
)

const migrationAdvisoryLock = 608731902

type postgresMigration struct {
	version    string
	name       string
	statements []string
}

func (s *PostgresStore) bootstrap(ctx context.Context) error {
	if err := s.applyMigrations(ctx); err != nil {
		return err
	}
	if err := s.seedDefaults(ctx); err != nil {
		return err
	}
	if err := s.ensureBootstrapQualityRelease(ctx); err != nil {
		return err
	}
	if err := s.reconcileLegacyKnowledge(ctx); err != nil {
		return err
	}
	return s.syncCounters(ctx)
}

func (s *PostgresStore) applyMigrations(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	for _, migration := range postgresMigrations() {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.version, err)
		}
		failed := func(cause error) error {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s_%s: %w", migration.version, migration.name, cause)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationAdvisoryLock); err != nil {
			return failed(err)
		}
		var applied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, migration.version).Scan(&applied); err != nil {
			return failed(err)
		}
		if applied {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit migration check %s: %w", migration.version, err)
			}
			continue
		}
		for _, statement := range migration.statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return failed(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, NOW())`, migration.version, migration.name); err != nil {
			return failed(err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s_%s: %w", migration.version, migration.name, err)
		}
	}
	return nil
}

func postgresMigrations() []postgresMigration {
	return []postgresMigration{
		{
			version: "001",
			name:    "legacy_core",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS see_agent_counters (
					name TEXT PRIMARY KEY,
					next_value BIGINT NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS agent_configs (
					config_version TEXT PRIMARY KEY,
					model_id TEXT NOT NULL,
					system_prompt_version TEXT NOT NULL,
					temperature DOUBLE PRECISION NOT NULL,
					max_output_tokens INTEGER NOT NULL,
					created_at TIMESTAMPTZ NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS retrieval_configs (
					retrieval_config_version TEXT PRIMARY KEY,
					embedding_model TEXT NOT NULL,
					top_k INTEGER NOT NULL,
					rerank_enabled BOOLEAN NOT NULL,
					created_at TIMESTAMPTZ NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS conversations (
					conversation_id TEXT PRIMARY KEY,
					title TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL,
					config_version TEXT NOT NULL,
					retrieval_config_version TEXT NOT NULL,
					knowledge_snapshot_id TEXT NOT NULL,
					model_id TEXT NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS messages (
					message_id TEXT PRIMARY KEY,
					conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE CASCADE,
					role TEXT NOT NULL,
					content TEXT NOT NULL,
					status TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL,
					finish_reason TEXT,
					citations_count INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE TABLE IF NOT EXISTS citations (
					message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
					rank INTEGER NOT NULL,
					chunk_id TEXT NOT NULL,
					file_id TEXT NOT NULL,
					filename TEXT NOT NULL,
					score DOUBLE PRECISION NOT NULL,
					citation_label TEXT NOT NULL,
					source_locator JSONB NOT NULL,
					snippet TEXT NOT NULL,
					PRIMARY KEY (message_id, rank, chunk_id)
				)`,
				`CREATE TABLE IF NOT EXISTS knowledge_files (
					file_id TEXT PRIMARY KEY,
					filename TEXT NOT NULL,
					mime_type TEXT NOT NULL,
					source_format TEXT NOT NULL,
					status TEXT NOT NULL,
					uploaded_by TEXT NOT NULL,
					uploaded_at TIMESTAMPTZ NOT NULL,
					object_uri TEXT NOT NULL,
					parse_version TEXT NOT NULL,
					index_version TEXT NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS ingestion_tasks (
					task_id TEXT PRIMARY KEY,
					file_id TEXT NOT NULL REFERENCES knowledge_files(file_id) ON DELETE CASCADE,
					status TEXT NOT NULL,
					requested_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL,
					force_reindex BOOLEAN NOT NULL DEFAULT FALSE,
					error_code TEXT,
					error_message TEXT,
					error_retryable BOOLEAN
				)`,
				`CREATE TABLE IF NOT EXISTS chunks (
					chunk_id TEXT PRIMARY KEY,
					file_id TEXT NOT NULL REFERENCES knowledge_files(file_id) ON DELETE CASCADE,
					source_format TEXT NOT NULL,
					block_type TEXT NOT NULL,
					heading_path JSONB,
					page_no INTEGER,
					source_locator JSONB NOT NULL,
					text TEXT NOT NULL,
					token_count INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX IF NOT EXISTS idx_conversations_updated_at ON conversations(updated_at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id, created_at ASC)`,
				`CREATE INDEX IF NOT EXISTS idx_files_uploaded_at ON knowledge_files(uploaded_at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_tasks_file_id ON ingestion_tasks(file_id, requested_at DESC)`,
			},
		},
		{
			version: "002",
			name:    "knowledge_evolution",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS knowledge_documents (
					document_id TEXT PRIMARY KEY,
					display_name TEXT NOT NULL,
					owner_id TEXT NOT NULL,
					status TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS knowledge_revisions (
					revision_id TEXT PRIMARY KEY,
					document_id TEXT NOT NULL REFERENCES knowledge_documents(document_id) ON DELETE RESTRICT,
					legacy_file_id TEXT REFERENCES knowledge_files(file_id) ON DELETE RESTRICT,
					filename TEXT NOT NULL,
					mime_type TEXT NOT NULL,
					source_format TEXT NOT NULL,
					object_uri TEXT NOT NULL,
					checksum_sha256 TEXT,
					parse_version TEXT NOT NULL,
					index_version TEXT NOT NULL,
					embedding_model TEXT,
					effective_at TIMESTAMPTZ,
					chunk_manifest_sha256 TEXT,
					status TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_revisions_legacy_file_id ON knowledge_revisions(legacy_file_id) WHERE legacy_file_id IS NOT NULL`,
				`CREATE INDEX IF NOT EXISTS idx_revisions_document_id ON knowledge_revisions(document_id, created_at DESC)`,
				`CREATE TABLE IF NOT EXISTS knowledge_revision_chunks (
					revision_id TEXT NOT NULL REFERENCES knowledge_revisions(revision_id) ON DELETE CASCADE,
					chunk_id TEXT NOT NULL,
					chunk_content_sha256 TEXT NOT NULL,
					source_locator JSONB NOT NULL,
					vector_point_id TEXT,
					PRIMARY KEY (revision_id, chunk_id)
				)`,
				`CREATE TABLE IF NOT EXISTS knowledge_snapshots (
					snapshot_id TEXT PRIMARY KEY,
					parent_snapshot_id TEXT REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					status TEXT NOT NULL,
					manifest_sha256 TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL,
					published_at TIMESTAMPTZ
				)`,
				`CREATE TABLE IF NOT EXISTS knowledge_snapshot_members (
					snapshot_id TEXT NOT NULL REFERENCES knowledge_snapshots(snapshot_id) ON DELETE CASCADE,
					revision_id TEXT NOT NULL REFERENCES knowledge_revisions(revision_id) ON DELETE RESTRICT,
					checksum_sha256 TEXT NOT NULL,
					PRIMARY KEY (snapshot_id, revision_id)
				)`,
				`CREATE TABLE IF NOT EXISTS knowledge_active_snapshot (
					singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
					snapshot_id TEXT REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					lock_version BIGINT NOT NULL DEFAULT 0
				)`,
				`INSERT INTO knowledge_active_snapshot (singleton, snapshot_id, lock_version)
					VALUES (TRUE, NULL, 0)
					ON CONFLICT (singleton) DO NOTHING`,
				`CREATE TABLE IF NOT EXISTS knowledge_update_events (
					event_id TEXT PRIMARY KEY,
					event_type TEXT NOT NULL,
					document_id TEXT REFERENCES knowledge_documents(document_id) ON DELETE RESTRICT,
					revision_id TEXT REFERENCES knowledge_revisions(revision_id) ON DELETE RESTRICT,
					snapshot_id TEXT REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					actor_id TEXT,
					correlation_id TEXT,
					diff_summary TEXT,
					effective_from TIMESTAMPTZ,
					effective_to TIMESTAMPTZ,
					occurred_at TIMESTAMPTZ NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_update_events_document_time ON knowledge_update_events(document_id, occurred_at DESC)`,
				`CREATE TABLE IF NOT EXISTS knowledge_outbox (
					delivery_id TEXT PRIMARY KEY,
					topic TEXT NOT NULL,
					document_id TEXT,
					revision_id TEXT,
					snapshot_id TEXT,
					payload JSONB NOT NULL,
					status TEXT NOT NULL,
					attempt_count INTEGER NOT NULL DEFAULT 0,
					available_at TIMESTAMPTZ NOT NULL,
					created_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_outbox_ready ON knowledge_outbox(status, available_at)`,
				`CREATE TABLE IF NOT EXISTS message_knowledge_provenance (
					message_id TEXT PRIMARY KEY REFERENCES messages(message_id) ON DELETE CASCADE,
					knowledge_snapshot_id TEXT NOT NULL REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					knowledge_snapshot_manifest_sha256 TEXT NOT NULL,
					answered_at TIMESTAMPTZ NOT NULL,
					freshness_status TEXT NOT NULL
				)`,
				`CREATE TABLE IF NOT EXISTS answer_revalidation_runs (
					revalidation_id TEXT PRIMARY KEY,
					conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE CASCADE,
					original_message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE RESTRICT,
					knowledge_snapshot_id TEXT REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					status TEXT NOT NULL,
					requested_at TIMESTAMPTZ NOT NULL,
					completed_at TIMESTAMPTZ
				)`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS knowledge_snapshot_id TEXT`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS knowledge_snapshot_manifest_hash TEXT`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS answered_at TIMESTAMPTZ`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS freshness_status TEXT`,
				`ALTER TABLE citations ADD COLUMN IF NOT EXISTS document_id TEXT`,
				`ALTER TABLE citations ADD COLUMN IF NOT EXISTS revision_id TEXT`,
				`ALTER TABLE citations ADD COLUMN IF NOT EXISTS chunk_content_sha256 TEXT`,
				`ALTER TABLE citations ADD COLUMN IF NOT EXISTS knowledge_snapshot_id TEXT`,
			},
		},
		{
			version: "003",
			name:    "knowledge_outbox_delivery",
			statements: []string{
				`ALTER TABLE knowledge_outbox ADD COLUMN IF NOT EXISTS stream_message_id TEXT`,
				`ALTER TABLE knowledge_outbox ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ`,
				`ALTER TABLE knowledge_outbox ADD COLUMN IF NOT EXISTS last_error TEXT`,
				`ALTER TABLE knowledge_outbox ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ`,
				`UPDATE knowledge_outbox SET status = 'pending' WHERE status IS NULL OR status = ''`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_outbox_stream_delivery ON knowledge_outbox(topic, status, available_at)`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_outbox_stream_message ON knowledge_outbox(stream_message_id) WHERE stream_message_id IS NOT NULL`,
			},
		},
		{
			version: "004",
			name:    "knowledge_lifecycle_idempotency",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS knowledge_mutation_idempotency (
					actor_id TEXT NOT NULL,
					operation TEXT NOT NULL,
					idempotency_key TEXT NOT NULL,
					request_hash TEXT NOT NULL,
					response_payload JSONB NOT NULL,
					created_at TIMESTAMPTZ NOT NULL,
					PRIMARY KEY (actor_id, operation, idempotency_key)
				)`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_documents_status_created ON knowledge_documents(status, created_at DESC, document_id DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_revisions_document_created ON knowledge_revisions(document_id, created_at DESC, revision_id DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_outbox_revision_delivery ON knowledge_outbox(revision_id, created_at DESC)`,
			},
		},
		{
			version: "005",
			name:    "knowledge_freshness_revalidation",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS knowledge_freshness_scans (
					event_id TEXT PRIMARY KEY REFERENCES knowledge_update_events(event_id) ON DELETE RESTRICT,
					status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'retry', 'completed', 'failed')),
					attempt_count INTEGER NOT NULL DEFAULT 0,
					available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					lease_until TIMESTAMPTZ,
					candidate_count INTEGER NOT NULL DEFAULT 0,
					marked_count INTEGER NOT NULL DEFAULT 0,
					last_error TEXT,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					completed_at TIMESTAMPTZ
				)`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_freshness_scans_claim ON knowledge_freshness_scans(status, available_at, lease_until)`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS requester_id TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS idempotency_key TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS trigger TEXT NOT NULL DEFAULT 'manual'`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS reason TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS latest_snapshot_id TEXT REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS latest_snapshot_manifest_sha256 TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS related_update_event_ids JSONB NOT NULL DEFAULT '[]'::jsonb`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS original_answer TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS original_snapshot_id TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS original_snapshot_manifest_sha256 TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS original_citation_revision_ids JSONB NOT NULL DEFAULT '[]'::jsonb`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 0`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS available_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS last_error_code TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS last_error_message TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS outcome TEXT`,
				`ALTER TABLE answer_revalidation_runs ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
				`UPDATE answer_revalidation_runs
					SET requester_id = COALESCE(NULLIF(requester_id, ''), 'legacy'),
						idempotency_key = COALESCE(NULLIF(idempotency_key, ''), revalidation_id),
						latest_snapshot_id = COALESCE(latest_snapshot_id, knowledge_snapshot_id),
						latest_snapshot_manifest_sha256 = COALESCE(latest_snapshot_manifest_sha256, ''),
						original_answer = COALESCE(original_answer, ''),
						original_snapshot_id = COALESCE(original_snapshot_id, ''),
						original_snapshot_manifest_sha256 = COALESCE(original_snapshot_manifest_sha256, '')`,
				`ALTER TABLE answer_revalidation_runs ALTER COLUMN requester_id SET NOT NULL`,
				`ALTER TABLE answer_revalidation_runs ALTER COLUMN idempotency_key SET NOT NULL`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_revalidation_request_idempotency ON answer_revalidation_runs(requester_id, idempotency_key)`,
				`CREATE INDEX IF NOT EXISTS idx_answer_revalidation_claim ON answer_revalidation_runs(status, available_at, lease_until)`,
				`CREATE INDEX IF NOT EXISTS idx_answer_revalidation_conversation ON answer_revalidation_runs(conversation_id, requested_at DESC)`,
				`CREATE TABLE IF NOT EXISTS answer_revalidation_results (
					revalidation_id TEXT PRIMARY KEY REFERENCES answer_revalidation_runs(revalidation_id) ON DELETE RESTRICT,
					content TEXT NOT NULL,
					knowledge_snapshot_id TEXT NOT NULL REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					knowledge_snapshot_manifest_sha256 TEXT NOT NULL,
					outcome TEXT NOT NULL CHECK (outcome IN ('still_valid', 'needs_update', 'insufficient_evidence')),
					citations JSONB NOT NULL,
					completed_at TIMESTAMPTZ NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
				)`,
			},
		},
		{
			version: "006",
			name:    "citation_anchors",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS message_citation_anchors (
					message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
					anchor_index INTEGER NOT NULL CHECK (anchor_index >= 0),
					start_rune INTEGER NOT NULL CHECK (start_rune >= 0),
					end_rune INTEGER NOT NULL CHECK (end_rune > start_rune),
					citation_ranks JSONB NOT NULL CHECK (jsonb_typeof(citation_ranks) = 'array'),
					PRIMARY KEY (message_id, anchor_index)
				)`,
				`CREATE INDEX IF NOT EXISTS idx_message_citation_anchors_message ON message_citation_anchors(message_id, anchor_index)`,
			},
		},
		{
			version: "007",
			name:    "k7_config_evidence",
			statements: []string{
				`ALTER TABLE agent_configs ADD COLUMN IF NOT EXISTS response_style TEXT NOT NULL DEFAULT 'concise'`,
				`ALTER TABLE agent_configs ADD COLUMN IF NOT EXISTS top_p DOUBLE PRECISION NOT NULL DEFAULT 1`,
				`CREATE TABLE IF NOT EXISTS message_execution_evidence (
					message_id TEXT PRIMARY KEY REFERENCES messages(message_id) ON DELETE CASCADE,
					conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE CASCADE,
					request_id TEXT NOT NULL,
					config_version TEXT NOT NULL REFERENCES agent_configs(config_version),
					retrieval_config_version TEXT NOT NULL REFERENCES retrieval_configs(retrieval_config_version),
					knowledge_snapshot_id TEXT NOT NULL REFERENCES knowledge_snapshots(snapshot_id),
					model_id TEXT NOT NULL,
					generation_config JSONB NOT NULL,
					retrieval_config JSONB NOT NULL,
					usage JSONB,
					refusal BOOLEAN NOT NULL,
					finish_reason TEXT NOT NULL,
					completed_at TIMESTAMPTZ NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
				)`,
				`CREATE INDEX IF NOT EXISTS idx_execution_evidence_conversation ON message_execution_evidence(conversation_id, completed_at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_execution_evidence_snapshot ON message_execution_evidence(knowledge_snapshot_id, completed_at DESC)`,
				`CREATE TABLE IF NOT EXISTS eval_reports (
					report_id TEXT PRIMARY KEY,
					idempotency_key TEXT NOT NULL UNIQUE,
					dataset_id TEXT NOT NULL,
					model_id TEXT NOT NULL,
					config_version TEXT NOT NULL,
					retrieval_config_version TEXT NOT NULL,
					generation_config JSONB NOT NULL,
					metrics JSONB NOT NULL,
					report JSONB NOT NULL,
					completed_at TIMESTAMPTZ,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
				)`,
				`CREATE INDEX IF NOT EXISTS idx_eval_reports_created_at ON eval_reports(created_at DESC)`,
			},
		},
		{
			version: "008",
			name:    "k7_eval_registration_hash",
			statements: []string{
				`ALTER TABLE eval_reports ADD COLUMN IF NOT EXISTS request_hash TEXT`,
				`UPDATE eval_reports SET request_hash = md5(report::text) WHERE request_hash IS NULL`,
				`ALTER TABLE eval_reports ALTER COLUMN request_hash SET NOT NULL`,
			},
		},
		{
			version: "009",
			name:    "c2_bounded_conversation_intent",
			statements: []string{
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS intent TEXT NOT NULL DEFAULT 'knowledge_query'`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS knowledge_used BOOLEAN NOT NULL DEFAULT TRUE`,
				`UPDATE messages SET knowledge_used = FALSE WHERE role <> 'assistant'`,
				`ALTER TABLE message_execution_evidence ALTER COLUMN knowledge_snapshot_id DROP NOT NULL`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS intent TEXT NOT NULL DEFAULT 'knowledge_query'`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS retrieval_performed BOOLEAN NOT NULL DEFAULT TRUE`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS generation_performed BOOLEAN NOT NULL DEFAULT TRUE`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS knowledge_used BOOLEAN NOT NULL DEFAULT TRUE`,
				`CREATE INDEX IF NOT EXISTS idx_execution_evidence_intent ON message_execution_evidence(intent, completed_at DESC)`,
			},
		},
		{
			version: "010",
			name:    "conversation_ownership",
			statements: []string{
				`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS owner_id TEXT`,
				`UPDATE conversations SET owner_id = 'user_admin' WHERE owner_id IS NULL OR BTRIM(owner_id) = ''`,
				`ALTER TABLE conversations ALTER COLUMN owner_id SET NOT NULL`,
				`CREATE INDEX IF NOT EXISTS idx_conversations_owner_updated ON conversations(owner_id, updated_at DESC, conversation_id DESC)`,
			},
		},
		{
			version: "011",
			name:    "retrieval_strategy_registry",
			statements: []string{
				`ALTER TABLE retrieval_configs ADD COLUMN IF NOT EXISTS retrieval_strategy TEXT`,
				`UPDATE retrieval_configs SET retrieval_strategy = 'lexical_compat_v1' WHERE retrieval_strategy IS NULL OR BTRIM(retrieval_strategy) = ''`,
				`ALTER TABLE retrieval_configs ALTER COLUMN retrieval_strategy SET NOT NULL`,
			},
		},
		{
			version: "012",
			name:    "quality_operations_release",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS answer_feedback (
					feedback_id TEXT PRIMARY KEY, message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE RESTRICT,
					conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE RESTRICT, actor_id TEXT NOT NULL,
					kind TEXT NOT NULL, comment TEXT NOT NULL DEFAULT '', corrected_answer TEXT NOT NULL DEFAULT '',
					expected_citation_ranks JSONB NOT NULL DEFAULT '[]'::jsonb, idempotency_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					CHECK (kind IN ('correct','incorrect','should_refuse','missing_evidence','wrong_evidence','outdated_knowledge','knowledge_gap'))
				)`,
				`CREATE INDEX IF NOT EXISTS idx_answer_feedback_message ON answer_feedback(message_id, created_at DESC)`,
				`CREATE TABLE IF NOT EXISTS quality_experiments (
					experiment_id TEXT PRIMARY KEY, status TEXT NOT NULL, created_by TEXT NOT NULL,
					baseline_config_version TEXT NOT NULL REFERENCES agent_configs(config_version), baseline_retrieval_config_version TEXT NOT NULL REFERENCES retrieval_configs(retrieval_config_version),
					candidate_config_version TEXT NOT NULL REFERENCES agent_configs(config_version), candidate_retrieval_config_version TEXT NOT NULL REFERENCES retrieval_configs(retrieval_config_version),
					knowledge_snapshot_id TEXT NOT NULL, knowledge_snapshot_manifest_hash TEXT NOT NULL, dataset_id TEXT NOT NULL, dataset_sha256 TEXT NOT NULL,
					conditions JSONB NOT NULL, conditions_sha256 TEXT NOT NULL, acceptance JSONB NOT NULL, idempotency_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL,
					baseline_report_id TEXT REFERENCES eval_reports(report_id), candidate_report_id TEXT REFERENCES eval_reports(report_id), decision_reason TEXT NOT NULL DEFAULT '',
					release_id TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), frozen_at TIMESTAMPTZ, approved_at TIMESTAMPTZ, published_at TIMESTAMPTZ,
					CHECK (status IN ('draft','collecting_feedback','ready_for_eval','evaluating','review_ready','approved','published','cancelled','failed','rejected','superseded'))
				)`,
				`CREATE TABLE IF NOT EXISTS quality_experiment_feedback (experiment_id TEXT NOT NULL REFERENCES quality_experiments(experiment_id) ON DELETE CASCADE, feedback_id TEXT NOT NULL REFERENCES answer_feedback(feedback_id) ON DELETE RESTRICT, PRIMARY KEY (experiment_id, feedback_id))`,
				`CREATE TABLE IF NOT EXISTS quality_releases (release_id TEXT PRIMARY KEY, status TEXT NOT NULL, config_version TEXT NOT NULL REFERENCES agent_configs(config_version), retrieval_config_version TEXT NOT NULL REFERENCES retrieval_configs(retrieval_config_version), experiment_id TEXT REFERENCES quality_experiments(experiment_id), previous_release_id TEXT REFERENCES quality_releases(release_id), created_by TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), CHECK(status IN ('published','rolled_back')) )`,
				`ALTER TABLE quality_experiments ADD CONSTRAINT quality_experiments_release_fk FOREIGN KEY (release_id) REFERENCES quality_releases(release_id)`,
				`CREATE TABLE IF NOT EXISTS quality_active_release (singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton), release_id TEXT NOT NULL REFERENCES quality_releases(release_id), lock_version BIGINT NOT NULL DEFAULT 0)`,
				`CREATE TABLE IF NOT EXISTS quality_release_events (event_id TEXT PRIMARY KEY, release_id TEXT NOT NULL REFERENCES quality_releases(release_id), event_type TEXT NOT NULL, actor_id TEXT NOT NULL, occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
				`CREATE TABLE IF NOT EXISTS quality_operation_requests (idempotency_key TEXT PRIMARY KEY, operation TEXT NOT NULL, resource_id TEXT NOT NULL, request_hash TEXT NOT NULL, response JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
			},
		},
		{
			version: "013",
			name:    "k8_evidence_min_score",
			statements: []string{
				`ALTER TABLE retrieval_configs ADD COLUMN IF NOT EXISTS evidence_min_score DOUBLE PRECISION NOT NULL DEFAULT 0.15`,
				`ALTER TABLE retrieval_configs ADD CONSTRAINT retrieval_configs_evidence_min_score_check CHECK (evidence_min_score > 0 AND evidence_min_score <= 1)`,
			},
		},
		{
			version: "014",
			name:    "onboarding_quality_gate",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS onboarding_datasets (
					dataset_id TEXT PRIMARY KEY,
					name TEXT NOT NULL,
					status TEXT NOT NULL,
					knowledge_snapshot_id TEXT NOT NULL REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					knowledge_snapshot_manifest_hash TEXT NOT NULL,
					cases_sha256 TEXT NOT NULL DEFAULT '',
					created_by TEXT NOT NULL,
					idempotency_key TEXT NOT NULL UNIQUE,
					request_hash TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					frozen_at TIMESTAMPTZ,
					CHECK (status IN ('draft','ready','superseded'))
				)`,
				`CREATE TABLE IF NOT EXISTS onboarding_cases (
					case_id TEXT PRIMARY KEY,
					dataset_id TEXT NOT NULL REFERENCES onboarding_datasets(dataset_id) ON DELETE CASCADE,
					case_type TEXT NOT NULL,
					question TEXT NOT NULL,
					expected_revision_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
					expected_locator_hints JSONB NOT NULL DEFAULT '[]'::jsonb,
					expected_answer_points JSONB NOT NULL DEFAULT '[]'::jsonb,
					review_status TEXT NOT NULL,
					reviewed_by TEXT NOT NULL DEFAULT '',
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					CHECK (case_type IN ('must_answer','must_refuse')),
					CHECK (review_status IN ('draft','reviewed'))
				)`,
				`CREATE INDEX IF NOT EXISTS idx_onboarding_cases_dataset ON onboarding_cases(dataset_id, created_at, case_id)`,
				`CREATE TABLE IF NOT EXISTS onboarding_runs (
					run_id TEXT PRIMARY KEY,
					dataset_id TEXT NOT NULL REFERENCES onboarding_datasets(dataset_id) ON DELETE RESTRICT,
					status TEXT NOT NULL,
					knowledge_snapshot_id TEXT NOT NULL REFERENCES knowledge_snapshots(snapshot_id) ON DELETE RESTRICT,
					knowledge_snapshot_manifest_hash TEXT NOT NULL,
					config_version TEXT NOT NULL REFERENCES agent_configs(config_version),
					retrieval_config_version TEXT NOT NULL REFERENCES retrieval_configs(retrieval_config_version),
					cases_sha256 TEXT NOT NULL,
					metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
					error_code TEXT NOT NULL DEFAULT '',
					error_message TEXT NOT NULL DEFAULT '',
					idempotency_key TEXT NOT NULL UNIQUE,
					request_hash TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					started_at TIMESTAMPTZ,
					completed_at TIMESTAMPTZ,
					lease_until TIMESTAMPTZ,
					CHECK (status IN ('queued','running','passed','failed'))
				)`,
				`CREATE INDEX IF NOT EXISTS idx_onboarding_runs_claim ON onboarding_runs(status, created_at)`,
				`CREATE TABLE IF NOT EXISTS onboarding_case_results (
					run_id TEXT NOT NULL REFERENCES onboarding_runs(run_id) ON DELETE CASCADE,
					case_id TEXT NOT NULL REFERENCES onboarding_cases(case_id) ON DELETE RESTRICT,
					passed BOOLEAN NOT NULL,
					refusal BOOLEAN NOT NULL,
					observed_answer TEXT NOT NULL DEFAULT '',
					observed_citations JSONB NOT NULL DEFAULT '[]'::jsonb,
					expected_revision_hit BOOLEAN,
					expected_locator_hit BOOLEAN,
					answer_point_hit_rate DOUBLE PRECISION,
					failure_code TEXT NOT NULL DEFAULT '',
					completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
					PRIMARY KEY (run_id, case_id)
				)`,
			},
		},
		{
			version: "015",
			name:    "onboarding_operation_idempotency",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS onboarding_operation_requests (
					idempotency_key TEXT PRIMARY KEY,
					operation TEXT NOT NULL,
					resource_id TEXT NOT NULL,
					request_hash TEXT NOT NULL,
					response JSONB NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
				)`,
			},
		},
		{
			version: "016",
			name:    "onboarding_counters",
			statements: []string{
				`INSERT INTO see_agent_counters(name,next_value) VALUES ('onboarding_dataset',1),('onboarding_case',1),('onboarding_run',1) ON CONFLICT (name) DO NOTHING`,
			},
		},
		{
			version: "017",
			name:    "conversation_title_source",
			statements: []string{
				`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS title_source TEXT`,
				`UPDATE conversations SET title_source = 'legacy' WHERE title_source IS NULL OR BTRIM(title_source) = ''`,
				`ALTER TABLE conversations ALTER COLUMN title_source SET NOT NULL`,
				`ALTER TABLE conversations ADD CONSTRAINT conversations_title_source_check CHECK (title_source IN ('system_default', 'first_user_message', 'user_edited', 'legacy'))`,
			},
		},
		{
			version: "018",
			name:    "fail_closed_chat_execution_attempts",
			statements: []string{
				`CREATE TABLE IF NOT EXISTS chat_execution_attempts (
					attempt_id TEXT PRIMARY KEY,
					request_id TEXT NOT NULL,
					parent_attempt_id TEXT REFERENCES chat_execution_attempts(attempt_id) ON DELETE RESTRICT,
					conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE CASCADE,
					user_message_id TEXT,
					assistant_message_id TEXT NOT NULL,
					state TEXT NOT NULL,
					config_version TEXT NOT NULL REFERENCES agent_configs(config_version),
					retrieval_config_version TEXT NOT NULL REFERENCES retrieval_configs(retrieval_config_version),
					knowledge_snapshot_id TEXT,
					knowledge_snapshot_manifest_hash TEXT,
					error_code TEXT,
					error_stage TEXT,
					retryable BOOLEAN NOT NULL DEFAULT FALSE,
					accepted_at TIMESTAMPTZ NOT NULL,
					started_at TIMESTAMPTZ,
					terminal_at TIMESTAMPTZ,
					CHECK (state IN ('accepted','running','succeeded','failed','cancelled')),
					CHECK ((state IN ('accepted','running') AND terminal_at IS NULL) OR (state IN ('succeeded','failed','cancelled') AND terminal_at IS NOT NULL)),
					CHECK ((state NOT IN ('failed','cancelled')) OR (BTRIM(COALESCE(error_code, '')) <> '' AND BTRIM(COALESCE(error_stage, '')) <> ''))
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_execution_attempts_conversation_request ON chat_execution_attempts(conversation_id, request_id)`,
				`CREATE INDEX IF NOT EXISTS idx_chat_execution_attempts_conversation_accepted ON chat_execution_attempts(conversation_id, accepted_at DESC)`,
			},
		},
		{
			version: "019",
			name:    "strict_runtime_execution_observation",
			statements: []string{
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS answer_disposition TEXT`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS answer_policy_version TEXT`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS execution_observation JSONB`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS used_citation_ranks JSONB NOT NULL DEFAULT '[]'::jsonb`,
			},
		},
		{
			version: "020",
			name:    "quality_history_eligibility",
			statements: []string{
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS legacy_fallback BOOLEAN NOT NULL DEFAULT FALSE`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS invalid_for_quality BOOLEAN NOT NULL DEFAULT FALSE`,
				`ALTER TABLE eval_reports ADD COLUMN IF NOT EXISTS legacy_fallback BOOLEAN NOT NULL DEFAULT FALSE`,
				`ALTER TABLE eval_reports ADD COLUMN IF NOT EXISTS invalid_for_quality BOOLEAN NOT NULL DEFAULT FALSE`,
				`UPDATE message_execution_evidence SET legacy_fallback=TRUE, invalid_for_quality=TRUE WHERE execution_observation IS NULL`,
				`UPDATE eval_reports SET legacy_fallback=TRUE, invalid_for_quality=TRUE`,
			},
		},
		{
			version: "021",
			name:    "canonical_lexical_retrieval_config",
			statements: []string{
				`INSERT INTO retrieval_configs (retrieval_config_version,retrieval_strategy,embedding_model,top_k,rerank_enabled,evidence_min_score,created_at) VALUES ('rcfg_lexical_v2','lexical_compat_v1','baseline-lexical-embedder',6,TRUE,0.15,NOW()) ON CONFLICT (retrieval_config_version) DO NOTHING`,
			},
		},
		{
			version: "022",
			name:    "retire_unreferenced_lexical_bootstrap_config",
			statements: []string{
				`DELETE FROM retrieval_configs legacy
				 WHERE legacy.retrieval_config_version = 'rcfg_lexical_v2'
				   AND NOT EXISTS (SELECT 1 FROM quality_releases release WHERE release.retrieval_config_version = legacy.retrieval_config_version)
				   AND NOT EXISTS (SELECT 1 FROM conversations conversation WHERE conversation.retrieval_config_version = legacy.retrieval_config_version)
				   AND NOT EXISTS (SELECT 1 FROM quality_experiments experiment WHERE experiment.baseline_retrieval_config_version = legacy.retrieval_config_version OR experiment.candidate_retrieval_config_version = legacy.retrieval_config_version)
				   AND NOT EXISTS (SELECT 1 FROM eval_reports report WHERE report.retrieval_config_version = legacy.retrieval_config_version)`,
			},
		},
		{
			version: "023",
			name:    "conversation_context_resolution_evidence",
			statements: []string{
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS context_resolution JSONB`,
			},
		},
		{
			version: "024",
			name:    "a1_clarification_evidence_contract",
			statements: []string{
				`ALTER TABLE agent_configs ADD COLUMN IF NOT EXISTS answer_policy_version TEXT`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS answer_disposition TEXT`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS answer_policy_version TEXT`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS policy_evaluation_performed BOOLEAN`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS answer_generation_performed BOOLEAN`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS coverage JSONB`,
				`CREATE TABLE IF NOT EXISTS conversation_clarifications (
					clarification_id TEXT PRIMARY KEY,
					conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE CASCADE,
					original_user_message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE RESTRICT,
					original_query TEXT NOT NULL,
					assistant_message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE RESTRICT,
					missing_dimensions JSONB NOT NULL,
					question TEXT NOT NULL,
					status TEXT NOT NULL,
					state_version INTEGER NOT NULL,
					turn_count INTEGER NOT NULL,
					resolution_user_message_id TEXT REFERENCES messages(message_id) ON DELETE RESTRICT,
					resolved_query TEXT,
					terminal_reason TEXT,
					created_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL,
					resolved_at TIMESTAMPTZ,
					CHECK (status IN ('pending','resolved','abandoned','expired')),
					CHECK (state_version >= 1),
					CHECK (turn_count >= 1),
					CHECK (jsonb_typeof(missing_dimensions) = 'array')
				)`,
				`ALTER TABLE messages ADD COLUMN IF NOT EXISTS clarification_id TEXT REFERENCES conversation_clarifications(clarification_id) DEFERRABLE INITIALLY DEFERRED`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS policy_evaluation_performed BOOLEAN`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS answer_generation_performed BOOLEAN`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS clarification JSONB`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS coverage JSONB`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS clarification_resolution JSONB`,
				`ALTER TABLE message_execution_evidence ADD COLUMN IF NOT EXISTS resolved_query TEXT`,
				`CREATE TABLE IF NOT EXISTS message_retrieval_candidates (
					message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
					rank INTEGER NOT NULL,
					chunk_id TEXT NOT NULL,
					file_id TEXT NOT NULL,
					document_id TEXT NOT NULL,
					revision_id TEXT NOT NULL,
					chunk_content_sha256 TEXT NOT NULL,
					knowledge_snapshot_id TEXT NOT NULL,
					score DOUBLE PRECISION NOT NULL,
					source_locator JSONB NOT NULL,
					PRIMARY KEY (message_id, rank)
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_conversation_clarifications_pending ON conversation_clarifications(conversation_id) WHERE status = 'pending'`,
				`CREATE INDEX IF NOT EXISTS idx_conversation_clarifications_status ON conversation_clarifications(conversation_id, status, updated_at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_retrieval_candidates_revision ON message_retrieval_candidates(revision_id)`,
				`INSERT INTO see_agent_counters(name,next_value) VALUES ('clarification', 1) ON CONFLICT (name) DO NOTHING`,
			},
		},
		{
			version: "025",
			name:    "a1_quality_feedback_kinds",
			statements: []string{
				`ALTER TABLE answer_feedback DROP CONSTRAINT IF EXISTS answer_feedback_kind_check`,
				`ALTER TABLE answer_feedback ADD CONSTRAINT answer_feedback_kind_check CHECK (kind IN ('correct','incorrect','should_refuse','missing_evidence','wrong_evidence','outdated_knowledge','knowledge_gap','should_clarify','unnecessary_clarification','incomplete_coverage'))`,
			},
		},
	}
}

func (s *PostgresStore) reconcileLegacyKnowledge(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy reconciliation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationAdvisoryLock); err != nil {
		return fmt.Errorf("lock legacy reconciliation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_documents (document_id, display_name, owner_id, status, created_at, updated_at)
		SELECT 'doc_legacy_' || file_id, filename, uploaded_by, 'active', uploaded_at, uploaded_at
		FROM knowledge_files
		WHERE status = 'succeeded'
		ON CONFLICT (document_id) DO NOTHING`); err != nil {
		return fmt.Errorf("reconcile legacy documents: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_revisions (
			revision_id, document_id, legacy_file_id, filename, mime_type, source_format, object_uri,
			checksum_sha256, parse_version, index_version, status, created_at, updated_at
		)
		SELECT
			'rev_legacy_' || file_id, 'doc_legacy_' || file_id, file_id, filename, mime_type, source_format, object_uri,
			NULL, parse_version, index_version, 'legacy_unpublished', uploaded_at, uploaded_at
		FROM knowledge_files
		WHERE status = 'succeeded'
		ON CONFLICT (legacy_file_id) WHERE legacy_file_id IS NOT NULL DO NOTHING`); err != nil {
		return fmt.Errorf("reconcile legacy revisions: %w", err)
	}
	return tx.Commit()
}
