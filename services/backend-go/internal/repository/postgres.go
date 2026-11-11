package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

type PostgresStore struct {
	db          *sql.DB
	publishHook func(stage string) error
}

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	store := &PostgresStore{db: db}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := store.bootstrap(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *PostgresStore) ActiveConfig() domain.AgentConfig {
	var config domain.AgentConfig
	row := s.db.QueryRowContext(context.Background(), `
		SELECT agent_configs.config_version, agent_configs.model_id, agent_configs.system_prompt_version, agent_configs.response_style, agent_configs.temperature, agent_configs.top_p, agent_configs.max_output_tokens, COALESCE(agent_configs.answer_policy_version, 'legacy_unknown'), agent_configs.created_at
		FROM quality_active_release pointer
		JOIN quality_releases release ON release.release_id = pointer.release_id
		JOIN agent_configs ON agent_configs.config_version = release.config_version`)
	if err := scanAgentConfig(row, &config); err != nil {
		return domain.AgentConfig{}
	}
	return config
}

func (s *PostgresStore) GetConfig(configVersion string) (domain.AgentConfig, bool) {
	var config domain.AgentConfig
	row := s.db.QueryRowContext(context.Background(), `
		SELECT config_version, model_id, system_prompt_version, response_style, temperature, top_p, max_output_tokens, COALESCE(answer_policy_version, 'legacy_unknown'), created_at
		FROM agent_configs
		WHERE config_version = $1`, configVersion)
	if err := scanAgentConfig(row, &config); err != nil {
		return domain.AgentConfig{}, false
	}
	return config, true
}

func (s *PostgresStore) CreateConfig(modelID, promptVersion, responseStyle, answerPolicyVersion string, temperature, topP float64, maxOutputTokens int) domain.AgentConfig {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AgentConfig{}
	}
	defer tx.Rollback()

	next, err := nextCounterTx(ctx, tx, "config")
	if err != nil {
		return domain.AgentConfig{}
	}
	answerPolicyVersion = strings.TrimSpace(answerPolicyVersion)
	displayPolicyVersion := answerPolicyVersion
	if displayPolicyVersion == "" {
		displayPolicyVersion = domain.LegacyAnswerPolicyVersion
	}
	config := domain.AgentConfig{
		ConfigVersion:       fmt.Sprintf("cfg_%03d", next),
		ModelID:             modelID,
		SystemPromptVersion: promptVersion,
		ResponseStyle:       responseStyle,
		Temperature:         temperature,
		TopP:                topP,
		MaxOutputTokens:     maxOutputTokens,
		AnswerPolicyVersion: displayPolicyVersion,
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_configs (config_version, model_id, system_prompt_version, response_style, temperature, top_p, max_output_tokens, answer_policy_version, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9)`,
		config.ConfigVersion, config.ModelID, config.SystemPromptVersion, config.ResponseStyle, config.Temperature, config.TopP, config.MaxOutputTokens, answerPolicyVersion, config.CreatedAt)
	if err != nil {
		return domain.AgentConfig{}
	}
	if err := tx.Commit(); err != nil {
		return domain.AgentConfig{}
	}
	return config
}

func (s *PostgresStore) ListConfigs() []domain.AgentConfig {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT config_version, model_id, system_prompt_version, response_style, temperature, top_p, max_output_tokens, COALESCE(answer_policy_version, 'legacy_unknown'), created_at
		FROM agent_configs
		ORDER BY created_at ASC, config_version ASC`)
	if err != nil {
		return []domain.AgentConfig{}
	}
	defer rows.Close()

	items := []domain.AgentConfig{}
	for rows.Next() {
		var item domain.AgentConfig
		if err := scanAgentConfig(rows, &item); err != nil {
			return []domain.AgentConfig{}
		}
		items = append(items, item)
	}
	return items
}

func (s *PostgresStore) ActiveRetrievalConfig() domain.RetrievalConfig {
	var config domain.RetrievalConfig
	row := s.db.QueryRowContext(context.Background(), `
		SELECT retrieval_configs.retrieval_config_version, retrieval_configs.retrieval_strategy, retrieval_configs.embedding_model, retrieval_configs.top_k, retrieval_configs.rerank_enabled, retrieval_configs.evidence_min_score, retrieval_configs.created_at
		FROM quality_active_release pointer
		JOIN quality_releases release ON release.release_id = pointer.release_id
		JOIN retrieval_configs ON retrieval_configs.retrieval_config_version = release.retrieval_config_version`)
	if err := scanRetrievalConfig(row, &config); err != nil {
		return domain.RetrievalConfig{}
	}
	return config
}

func (s *PostgresStore) GetRetrievalConfig(retrievalConfigVersion string) (domain.RetrievalConfig, bool) {
	var config domain.RetrievalConfig
	row := s.db.QueryRowContext(context.Background(), `
		SELECT retrieval_config_version, retrieval_strategy, embedding_model, top_k, rerank_enabled, evidence_min_score, created_at
		FROM retrieval_configs
		WHERE retrieval_config_version = $1`, retrievalConfigVersion)
	if err := scanRetrievalConfig(row, &config); err != nil {
		return domain.RetrievalConfig{}, false
	}
	return config, true
}

func (s *PostgresStore) CreateRetrievalConfig(retrievalStrategy, embeddingModel string, topK int, rerankEnabled bool, evidenceMinScore float64) domain.RetrievalConfig {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RetrievalConfig{}
	}
	defer tx.Rollback()

	next, err := nextCounterTx(ctx, tx, "retrieval")
	if err != nil {
		return domain.RetrievalConfig{}
	}
	config := domain.RetrievalConfig{
		RetrievalConfigVersion: fmt.Sprintf("rcfg_%03d", next),
		RetrievalStrategy:      retrievalStrategy,
		EmbeddingModel:         embeddingModel,
		TopK:                   topK,
		RerankEnabled:          rerankEnabled,
		EvidenceMinScore:       evidenceMinScore,
		CreatedAt:              time.Now().UTC().Format(time.RFC3339),
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO retrieval_configs (retrieval_config_version, retrieval_strategy, embedding_model, top_k, rerank_enabled, evidence_min_score, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		config.RetrievalConfigVersion, config.RetrievalStrategy, config.EmbeddingModel, config.TopK, config.RerankEnabled, config.EvidenceMinScore, config.CreatedAt)
	if err != nil {
		return domain.RetrievalConfig{}
	}
	if err := tx.Commit(); err != nil {
		return domain.RetrievalConfig{}
	}
	return config
}

func (s *PostgresStore) ListRetrievalConfigs() []domain.RetrievalConfig {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT retrieval_config_version, retrieval_strategy, embedding_model, top_k, rerank_enabled, evidence_min_score, created_at
		FROM retrieval_configs
		ORDER BY created_at ASC, retrieval_config_version ASC`)
	if err != nil {
		return []domain.RetrievalConfig{}
	}
	defer rows.Close()

	items := []domain.RetrievalConfig{}
	for rows.Next() {
		var item domain.RetrievalConfig
		if err := rows.Scan(&item.RetrievalConfigVersion, &item.RetrievalStrategy, &item.EmbeddingModel, &item.TopK, &item.RerankEnabled, &item.EvidenceMinScore, &item.CreatedAt); err != nil {
			return []domain.RetrievalConfig{}
		}
		items = append(items, item)
	}
	return items
}

func (s *PostgresStore) CreateConversation(title string) domain.Conversation {
	return s.CreateOwnedConversation("user_admin", title)
}

func (s *PostgresStore) CreateOwnedConversation(ownerID, title string) domain.Conversation {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return domain.Conversation{}
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Conversation{}
	}
	defer tx.Rollback()

	activeConfig, activeRetrieval, err := activeQualityConfigPairTx(ctx, tx)
	if err != nil {
		return domain.Conversation{}
	}
	next, err := nextCounterTx(ctx, tx, "conversation")
	if err != nil {
		return domain.Conversation{}
	}
	titleSource := ConversationTitleUserEdited
	if strings.TrimSpace(title) == "" {
		title = DefaultConversationTitle
		titleSource = ConversationTitleSystemDefault
	}
	now := time.Now().UTC().Format(time.RFC3339)
	conversation := domain.Conversation{
		ConversationID:         fmt.Sprintf("conv_%03d", next),
		OwnerID:                ownerID,
		Title:                  title,
		TitleSource:            titleSource,
		CreatedAt:              now,
		UpdatedAt:              now,
		ConfigVersion:          activeConfig.ConfigVersion,
		RetrievalConfigVersion: activeRetrieval.RetrievalConfigVersion,
		// A conversation does not own a knowledge snapshot. Each knowledge turn
		// resolves and persists its immutable active scope at execution time.
		KnowledgeSnapshotID: "",
		ModelID:             activeConfig.ModelID,
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO conversations (conversation_id, owner_id, title, title_source, created_at, updated_at, config_version, retrieval_config_version, knowledge_snapshot_id, model_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		conversation.ConversationID, conversation.OwnerID, conversation.Title, conversation.TitleSource, conversation.CreatedAt,
		conversation.UpdatedAt, conversation.ConfigVersion, conversation.RetrievalConfigVersion, conversation.KnowledgeSnapshotID, conversation.ModelID)
	if err != nil {
		return domain.Conversation{}
	}
	if err := tx.Commit(); err != nil {
		return domain.Conversation{}
	}
	return conversation
}

func (s *PostgresStore) ListOwnedConversations(ownerID string) []domain.Conversation {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return []domain.Conversation{}
	}
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT conversation_id, owner_id, title, title_source, created_at, updated_at, config_version, retrieval_config_version, knowledge_snapshot_id, model_id
		FROM conversations
		WHERE owner_id = $1
		ORDER BY updated_at DESC, conversation_id DESC`, ownerID)
	if err != nil {
		return []domain.Conversation{}
	}
	defer rows.Close()
	items := []domain.Conversation{}
	for rows.Next() {
		var item domain.Conversation
		if err := scanConversation(rows, &item); err != nil {
			return []domain.Conversation{}
		}
		items = append(items, item)
	}
	return items
}

func (s *PostgresStore) ListConversations() []domain.Conversation {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT conversation_id, owner_id, title, title_source, created_at, updated_at, config_version, retrieval_config_version, knowledge_snapshot_id, model_id
		FROM conversations
		ORDER BY updated_at DESC, conversation_id DESC`)
	if err != nil {
		return []domain.Conversation{}
	}
	defer rows.Close()

	items := []domain.Conversation{}
	for rows.Next() {
		var item domain.Conversation
		if err := scanConversation(rows, &item); err != nil {
			return []domain.Conversation{}
		}
		items = append(items, item)
	}
	return items
}

func (s *PostgresStore) GetConversation(id string) (domain.Conversation, []domain.Message, bool) {
	ctx := context.Background()
	var conversation domain.Conversation
	row := s.db.QueryRowContext(ctx, `
		SELECT conversation_id, owner_id, title, title_source, created_at, updated_at, config_version, retrieval_config_version, knowledge_snapshot_id, model_id
		FROM conversations
		WHERE conversation_id = $1`, id)
	if err := scanConversation(row, &conversation); err != nil {
		return domain.Conversation{}, nil, false
	}

	messages, err := s.listMessages(ctx, id)
	if err != nil {
		return domain.Conversation{}, nil, false
	}
	return conversation, messages, true
}

func (s *PostgresStore) GetOwnedConversation(ownerID, conversationID string) (domain.Conversation, []domain.Message, bool) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return domain.Conversation{}, nil, false
	}
	ctx := context.Background()
	var conversation domain.Conversation
	row := s.db.QueryRowContext(ctx, `
		SELECT conversation_id, owner_id, title, title_source, created_at, updated_at, config_version, retrieval_config_version, knowledge_snapshot_id, model_id
		FROM conversations
		WHERE conversation_id = $1 AND owner_id = $2`, conversationID, ownerID)
	if err := scanConversation(row, &conversation); err != nil {
		return domain.Conversation{}, nil, false
	}
	messages, err := s.listMessages(ctx, conversationID)
	if err != nil {
		return domain.Conversation{}, nil, false
	}
	return conversation, messages, true
}

func (s *PostgresStore) RenameOwnedConversation(ownerID, conversationID, title string) (domain.Conversation, bool) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return domain.Conversation{}, false
	}
	var conversation domain.Conversation
	var createdAt, updatedAt time.Time
	err := s.db.QueryRowContext(context.Background(), `
		UPDATE conversations
		SET title = $3, title_source = $4, updated_at = NOW()
		WHERE conversation_id = $1 AND owner_id = $2
		RETURNING conversation_id, owner_id, title, title_source, created_at, updated_at, config_version, retrieval_config_version, knowledge_snapshot_id, model_id`,
		conversationID, ownerID, title, ConversationTitleUserEdited,
	).Scan(
		&conversation.ConversationID, &conversation.OwnerID, &conversation.Title, &conversation.TitleSource,
		&createdAt, &updatedAt, &conversation.ConfigVersion, &conversation.RetrievalConfigVersion,
		&conversation.KnowledgeSnapshotID, &conversation.ModelID,
	)
	if err != nil {
		return domain.Conversation{}, false
	}
	conversation.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	conversation.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return conversation, true
}

func (s *PostgresStore) ReserveMessageID() string {
	next, err := nextCounter(context.Background(), s.db, "message")
	if err != nil {
		return ""
	}
	return fmt.Sprintf("msg_%03d", next)
}

func (s *PostgresStore) AppendRuntimeExchange(conversationID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, bool) {
	return s.AppendRuntimeExchangeWithIDs(conversationID, "", content, result)
}

func (s *PostgresStore) AppendRuntimeExchangeWithIDs(conversationID, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, bool) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}
	defer tx.Rollback()

	conversation, err := conversationForUpdateTx(ctx, tx, conversationID)
	if err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	userMessageID = strings.TrimSpace(userMessageID)
	if userMessageID == "" {
		userID, err := nextCounterTx(ctx, tx, "message")
		if err != nil {
			return domain.Message{}, domain.Message{}, nil, false
		}
		userMessageID = fmt.Sprintf("msg_%03d", userID)
	}
	userMessage := domain.Message{
		MessageID:      userMessageID,
		ConversationID: conversationID,
		Role:           "user",
		Content:        content,
		Status:         "completed",
		CreatedAt:      now,
		Intent:         normalizedIntent(result.Intent),
		KnowledgeUsed:  false,
	}
	if err := insertMessageTx(ctx, tx, userMessage); err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}

	assistantMessageID := strings.TrimSpace(result.MessageID)
	finishReason := strings.TrimSpace(result.FinishReason)
	assistantContent := strings.TrimSpace(result.AssistantMessage.Content)
	if assistantMessageID == "" || finishReason == "" || assistantContent == "" {
		return domain.Message{}, domain.Message{}, nil, false
	}
	citations := make([]domain.Citation, 0, len(result.Citations))
	seenRanks := make(map[int]struct{}, len(result.Citations))
	for _, item := range result.Citations {
		if item.Rank < 1 || strings.TrimSpace(item.CitationLabel) == "" {
			return domain.Message{}, domain.Message{}, nil, false
		}
		if _, exists := seenRanks[item.Rank]; exists {
			return domain.Message{}, domain.Message{}, nil, false
		}
		seenRanks[item.Rank] = struct{}{}
		citations = append(citations, domain.Citation{
			MessageID:     assistantMessageID,
			ChunkID:       item.ChunkID,
			FileID:        item.FileID,
			Filename:      item.Filename,
			Rank:          item.Rank,
			Score:         item.Score,
			CitationLabel: item.CitationLabel,
			SourceLocator: item.SourceLocator,
			Snippet:       item.Snippet,
		})
	}
	assistantMessage := domain.Message{
		MessageID:       assistantMessageID,
		ConversationID:  conversationID,
		Role:            "assistant",
		Content:         assistantContent,
		Status:          "completed",
		CreatedAt:       coalesceTimestamp(result.CompletedAt),
		FinishReason:    &finishReason,
		CitationsCount:  len(citations),
		CitationAnchors: result.CitationAnchors,
		Intent:          normalizedIntent(result.Intent),
		KnowledgeUsed:   result.KnowledgeUsed,
	}
	if err := insertMessageTx(ctx, tx, assistantMessage); err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}
	for _, citation := range citations {
		if err := insertCitationTx(ctx, tx, citation); err != nil {
			return domain.Message{}, domain.Message{}, nil, false
		}
	}
	if err := insertCitationAnchorsTx(ctx, tx, assistantMessage.MessageID, result.CitationAnchors); err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}
	applyAutomaticConversationTitle(&conversation, content)
	conversation.UpdatedAt = now
	if err := updateConversationTitleAndTimestampTx(ctx, tx, conversation); err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}
	if err := completeChatExecutionAttemptTx(ctx, tx, result.RequestID); err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}
	if err := tx.Commit(); err != nil {
		return domain.Message{}, domain.Message{}, nil, false
	}
	return userMessage, assistantMessage, citations, true
}

func (s *PostgresStore) GetSources(messageID string) []domain.Citation {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT message_id, chunk_id, file_id, document_id, revision_id, chunk_content_sha256, knowledge_snapshot_id,
			filename, rank, score, citation_label, source_locator, snippet
		FROM citations
		WHERE message_id = $1
		ORDER BY rank ASC`, messageID)
	if err != nil {
		return []domain.Citation{}
	}
	defer rows.Close()

	items := []domain.Citation{}
	for rows.Next() {
		item, err := scanCitation(rows)
		if err != nil {
			return []domain.Citation{}
		}
		items = append(items, item)
	}
	return items
}

func (s *PostgresStore) GetMessageSources(messageID string) (domain.MessageSources, bool) {
	var result domain.MessageSources
	var snapshotID, manifestHash, freshnessStatus sql.NullString
	err := s.db.QueryRowContext(context.Background(), `
		SELECT message_id, knowledge_snapshot_id, knowledge_snapshot_manifest_hash, freshness_status
		FROM messages
		WHERE message_id = $1 AND role = 'assistant'`, messageID,
	).Scan(&result.MessageID, &snapshotID, &manifestHash, &freshnessStatus)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return domain.MessageSources{}, false
	}
	result.KnowledgeSnapshotID = nullableString(snapshotID)
	result.KnowledgeSnapshotManifestHash = nullableString(manifestHash)
	result.FreshnessStatus = nullableString(freshnessStatus)
	result.Items = s.GetSources(messageID)
	if len(result.Items) == 0 {
		return domain.MessageSources{}, false
	}
	return result, true
}

func (s *PostgresStore) ListFiles(status, sourceFormat string) []domain.KnowledgeFile {
	query := `
		SELECT file_id, filename, mime_type, source_format, status, uploaded_by, uploaded_at, object_uri, parse_version, index_version
		FROM knowledge_files`
	args := []any{}
	clauses := []string{}
	if status != "" {
		args = append(args, status)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if sourceFormat != "" {
		args = append(args, sourceFormat)
		clauses = append(clauses, fmt.Sprintf("source_format = $%d", len(args)))
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY uploaded_at DESC, file_id DESC"

	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return []domain.KnowledgeFile{}
	}
	defer rows.Close()

	items := []domain.KnowledgeFile{}
	for rows.Next() {
		var item domain.KnowledgeFile
		if err := scanKnowledgeFile(rows, &item); err != nil {
			return []domain.KnowledgeFile{}
		}
		items = append(items, item)
	}
	return items
}

func (s *PostgresStore) ReserveFileID() string {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ""
	}
	defer tx.Rollback()
	next, err := nextCounterTx(ctx, tx, "file")
	if err != nil {
		return ""
	}
	if err := tx.Commit(); err != nil {
		return ""
	}
	return fmt.Sprintf("file_%03d", next)
}

func (s *PostgresStore) CreateFile(fileID, filename, providedMIMEType, objectURI string, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}
	}
	defer tx.Rollback()
	if strings.TrimSpace(fileID) == "" {
		next, err := nextCounterTx(ctx, tx, "file")
		if err != nil {
			return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}
		}
		fileID = fmt.Sprintf("file_%03d", next)
	}
	taskNext, err := nextCounterTx(ctx, tx, "task")
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sourceFormat, detectedMIMEType := detectFileShape(filename)
	mimeType := strings.TrimSpace(providedMIMEType)
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = detectedMIMEType
	}
	file := domain.KnowledgeFile{
		FileID:       fileID,
		Filename:     filename,
		MIMEType:     mimeType,
		SourceFormat: sourceFormat,
		Status:       "queued",
		UploadedBy:   "admin",
		UploadedAt:   now,
		ObjectURI:    objectURI,
		ParseVersion: "parse_v1",
		IndexVersion: "index_v1",
	}
	task := domain.IngestionTaskSummary{
		TaskID:       fmt.Sprintf("task_%03d", taskNext),
		FileID:       fileID,
		Status:       "queued",
		RequestedAt:  now,
		UpdatedAt:    now,
		ForceReindex: forceReindex,
	}
	if err := insertKnowledgeFileTx(ctx, tx, file); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}
	}
	if err := insertIngestionTaskTx(ctx, tx, task); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}
	}
	chunk := domain.ChunkRecord{
		ChunkID:      fmt.Sprintf("chunk_%s_001", fileID),
		FileID:       fileID,
		SourceFormat: sourceFormat,
		BlockType:    "placeholder",
		SourceLocator: domain.SourceLocator{
			SourceFormat: sourceFormat,
			BlockLabel:   "pending-ingestion",
		},
		Text:       "Chunk preview will be available after runtime ingestion is connected.",
		TokenCount: 10,
	}
	if err := replaceChunksTx(ctx, tx, fileID, []domain.ChunkRecord{chunk}); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}
	}
	return file, task
}

func (s *PostgresStore) MarkTaskRunning(fileID, taskID string) bool {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	latest, err := latestTaskTx(ctx, tx, fileID)
	if err != nil || latest.TaskID != taskID {
		return false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_files SET status = 'running' WHERE file_id = $1`, fileID); err != nil {
		return false
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ingestion_tasks SET status = 'running', updated_at = $3, error_code = NULL, error_message = NULL, error_retryable = NULL WHERE file_id = $1 AND task_id = $2`, fileID, taskID, now); err != nil {
		return false
	}
	return tx.Commit() == nil
}

func (s *PostgresStore) GetFile(fileID string) (domain.KnowledgeFile, domain.IngestionTaskSummary, bool) {
	ctx := context.Background()
	file, err := fileByID(ctx, s.db, fileID)
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	task, err := latestTask(ctx, s.db, fileID)
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	return file, task, true
}

func (s *PostgresStore) ReindexFile(fileID string, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary, bool) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	defer tx.Rollback()
	file, err := fileByIDTx(ctx, tx, fileID)
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	taskNext, err := nextCounterTx(ctx, tx, "task")
	if err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	file.Status = "queued"
	file.ParseVersion = nextVersion(file.ParseVersion, "parse")
	file.IndexVersion = nextVersion(file.IndexVersion, "index")
	task := domain.IngestionTaskSummary{
		TaskID:       fmt.Sprintf("task_%03d", taskNext),
		FileID:       fileID,
		Status:       "queued",
		RequestedAt:  now,
		UpdatedAt:    now,
		ForceReindex: forceReindex,
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_files SET status = $2, parse_version = $3, index_version = $4 WHERE file_id = $1`, file.FileID, file.Status, file.ParseVersion, file.IndexVersion); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	if err := insertIngestionTaskTx(ctx, tx, task); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	chunk := domain.ChunkRecord{
		ChunkID:      fmt.Sprintf("chunk_%s_queued", fileID),
		FileID:       fileID,
		SourceFormat: file.SourceFormat,
		BlockType:    "status",
		SourceLocator: domain.SourceLocator{
			SourceFormat: file.SourceFormat,
			BlockLabel:   "queued-for-reindex",
		},
		Text:       fmt.Sprintf("File %s is queued for reindex.", file.Filename),
		TokenCount: 7,
	}
	if err := replaceChunksTx(ctx, tx, fileID, []domain.ChunkRecord{chunk}); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	return file, task, true
}

func (s *PostgresStore) ListChunks(fileID string) ([]domain.ChunkRecord, bool) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT chunk_id, file_id, source_format, block_type, heading_path, page_no, source_locator, text, token_count
		FROM chunks
		WHERE file_id = $1
		ORDER BY chunk_id ASC`, fileID)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	items := []domain.ChunkRecord{}
	for rows.Next() {
		item, err := scanChunk(rows)
		if err != nil {
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

func (s *PostgresStore) ApplyIngestionResult(fileID, taskID string, result domain.IngestionTaskResult) bool {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	file, err := fileByIDTx(ctx, tx, fileID)
	if err != nil {
		return false
	}
	latest, err := latestTaskTx(ctx, tx, fileID)
	if err != nil || latest.TaskID != taskID {
		return false
	}
	status := result.Status
	if status == "" {
		status = "failed"
	}
	updatedAt := coalesceTimestamp(result.UpdatedAt)
	var errorCode, errorMessage sql.NullString
	var errorRetryable sql.NullBool
	if result.Error != nil {
		errorCode = sql.NullString{String: result.Error.Code, Valid: true}
		errorMessage = sql.NullString{String: result.Error.Message, Valid: true}
		errorRetryable = sql.NullBool{Bool: result.Error.Retryable, Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_files SET status = $2 WHERE file_id = $1`, fileID, status); err != nil {
		return false
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE ingestion_tasks
		SET status = $3, updated_at = $4, error_code = $5, error_message = $6, error_retryable = $7
		WHERE file_id = $1 AND task_id = $2`,
		fileID, taskID, status, updatedAt, errorCode, errorMessage, errorRetryable); err != nil {
		return false
	}
	switch status {
	case "succeeded":
		if err := replaceChunksTx(ctx, tx, fileID, buildSucceededChunks(file, result)); err != nil {
			return false
		}
	case "failed":
		if err := replaceChunksTx(ctx, tx, fileID, buildFailedChunks(file, result.Error)); err != nil {
			return false
		}
	}
	return tx.Commit() == nil
}

// legacyBootstrapCore is retained temporarily as the v1 DDL reference while K2 uses versioned migrations.
// It is intentionally not called by NewPostgresStore.
func (s *PostgresStore) legacyBootstrapCore(ctx context.Context) error {
	statements := []string{
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
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("bootstrap postgres schema: %w", err)
		}
	}
	if err := s.seedDefaults(ctx); err != nil {
		return err
	}
	return s.syncCounters(ctx)
}

func (s *PostgresStore) seedDefaults(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
	INSERT INTO agent_configs (config_version, model_id, system_prompt_version, response_style, temperature, top_p, max_output_tokens, created_at)
		SELECT 'cfg_001', $1, 'prompt_v7', 'concise', 0, 1, 1024, $2
		WHERE NOT EXISTS (SELECT 1 FROM agent_configs)
		ON CONFLICT DO NOTHING`, defaultAgentModelID(), now)
	if err != nil {
		return fmt.Errorf("seed default agent config: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO retrieval_configs (retrieval_config_version, retrieval_strategy, embedding_model, top_k, rerank_enabled, evidence_min_score, created_at)
		VALUES ('rcfg_001', 'native_dense_v1', 'qwen3-embedding:0.6b', 3, FALSE, 0.51, $1)
		ON CONFLICT (retrieval_config_version) DO NOTHING`, now)
	if err != nil {
		return fmt.Errorf("seed default retrieval config: %w", err)
	}
	// Knowledge must enter Postgres only through the administrator lifecycle.
	// In particular, a new fullstack database intentionally has no active
	// snapshot until upload, indexing, and explicit publish have completed.
	return nil
}

func (s *PostgresStore) syncCounters(ctx context.Context) error {
	counters := map[string]string{
		"conversation":       "SELECT COALESCE(MAX(substring(conversation_id from 'conv_([0-9]+)')::int), 0) + 1 FROM conversations",
		"message":            "SELECT COALESCE(MAX(substring(message_id from 'msg_([0-9]+)')::int), 0) + 1 FROM messages WHERE message_id ~ '^msg_[0-9]+$'",
		"file":               "SELECT GREATEST(COALESCE(MAX(substring(file_id from 'file_([0-9]+)')::int), 0) + 1, 2) FROM knowledge_files",
		"task":               "SELECT GREATEST(COALESCE(MAX(substring(task_id from 'task_([0-9]+)')::int), 0) + 1, 2) FROM ingestion_tasks",
		"config":             "SELECT GREATEST(COALESCE(MAX(substring(config_version from 'cfg_([0-9]+)')::int), 0) + 1, 2) FROM agent_configs",
		"retrieval":          "SELECT GREATEST(COALESCE(MAX(substring(retrieval_config_version from 'rcfg_([0-9]+)')::int), 0) + 1, 2) FROM retrieval_configs",
		"snapshot":           "SELECT COALESCE(MAX(substring(snapshot_id from 'snap_([0-9]+)')::int), 0) + 1 FROM knowledge_snapshots WHERE snapshot_id ~ '^snap_[0-9]+$'",
		"event":              "SELECT COALESCE(MAX(substring(event_id from 'event_([0-9]+)')::int), 0) + 1 FROM knowledge_update_events WHERE event_id ~ '^event_[0-9]+$'",
		"delivery":           "SELECT COALESCE(MAX(substring(delivery_id from 'delivery_([0-9]+)')::int), 0) + 1 FROM knowledge_outbox WHERE delivery_id ~ '^delivery_[0-9]+$'",
		"revalidation":       "SELECT COALESCE(MAX(substring(revalidation_id from 'reval_([0-9]+)')::int), 0) + 1 FROM answer_revalidation_runs WHERE revalidation_id ~ '^reval_[0-9]+$'",
		"quality_feedback":   "SELECT COALESCE(MAX(substring(feedback_id from 'qfb_([0-9]+)')::int), 0) + 1 FROM answer_feedback WHERE feedback_id ~ '^qfb_[0-9]+$'",
		"quality_experiment": "SELECT COALESCE(MAX(substring(experiment_id from 'qexp_([0-9]+)')::int), 0) + 1 FROM quality_experiments WHERE experiment_id ~ '^qexp_[0-9]+$'",
		"quality_release":    "SELECT COALESCE(MAX(substring(release_id from 'qrel_([0-9]+)')::int), 0) + 1 FROM quality_releases WHERE release_id ~ '^qrel_[0-9]+$'",
	}
	for name, query := range counters {
		var next int
		if err := s.db.QueryRowContext(ctx, query).Scan(&next); err != nil {
			return fmt.Errorf("sync %s counter: %w", name, err)
		}
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO see_agent_counters (name, next_value)
			VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET next_value = GREATEST(see_agent_counters.next_value, EXCLUDED.next_value)`,
			name, next)
		if err != nil {
			return fmt.Errorf("upsert %s counter: %w", name, err)
		}
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAgentConfig(row scanner, config *domain.AgentConfig) error {
	var createdAt time.Time
	if err := row.Scan(&config.ConfigVersion, &config.ModelID, &config.SystemPromptVersion, &config.ResponseStyle, &config.Temperature, &config.TopP, &config.MaxOutputTokens, &config.AnswerPolicyVersion, &createdAt); err != nil {
		return err
	}
	config.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return nil
}

func scanRetrievalConfig(row scanner, config *domain.RetrievalConfig) error {
	var createdAt time.Time
	if err := row.Scan(&config.RetrievalConfigVersion, &config.RetrievalStrategy, &config.EmbeddingModel, &config.TopK, &config.RerankEnabled, &config.EvidenceMinScore, &createdAt); err != nil {
		return err
	}
	config.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return nil
}

func scanConversation(row scanner, conversation *domain.Conversation) error {
	var createdAt, updatedAt time.Time
	if err := row.Scan(
		&conversation.ConversationID,
		&conversation.OwnerID,
		&conversation.Title,
		&conversation.TitleSource,
		&createdAt,
		&updatedAt,
		&conversation.ConfigVersion,
		&conversation.RetrievalConfigVersion,
		&conversation.KnowledgeSnapshotID,
		&conversation.ModelID,
	); err != nil {
		return err
	}
	conversation.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	conversation.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return nil
}

func scanMessage(row scanner) (domain.Message, error) {
	var message domain.Message
	var createdAt time.Time
	var finishReason, answerDisposition, answerPolicyVersion, clarificationID sql.NullString
	var snapshotID, manifestHash, freshnessStatus sql.NullString
	var answeredAt sql.NullTime
	var policyEvaluation, answerGeneration sql.NullBool
	var coveragePayload []byte
	err := row.Scan(
		&message.MessageID, &message.ConversationID, &message.Role, &message.Content, &message.Status, &createdAt, &finishReason, &message.CitationsCount,
		&snapshotID, &manifestHash, &answeredAt, &freshnessStatus, &message.Intent, &message.KnowledgeUsed,
		&answerDisposition, &answerPolicyVersion, &policyEvaluation, &answerGeneration, &clarificationID, &coveragePayload,
	)
	if err != nil {
		return domain.Message{}, err
	}
	message.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if finishReason.Valid {
		message.FinishReason = &finishReason.String
	}
	message.KnowledgeSnapshotID = nullableString(snapshotID)
	message.KnowledgeSnapshotManifestHash = nullableString(manifestHash)
	message.FreshnessStatus = nullableString(freshnessStatus)
	message.AnswerDisposition = nullableString(answerDisposition)
	message.AnswerPolicyVersion = nullableString(answerPolicyVersion)
	message.ClarificationID = nullableString(clarificationID)
	if policyEvaluation.Valid {
		value := policyEvaluation.Bool
		message.PolicyEvaluationPerformed = &value
	}
	if answerGeneration.Valid {
		value := answerGeneration.Bool
		message.AnswerGenerationPerformed = &value
	}
	if len(coveragePayload) > 0 {
		var coverage domain.RuntimeCoverage
		if err := json.Unmarshal(coveragePayload, &coverage); err != nil {
			return domain.Message{}, fmt.Errorf("decode message coverage: %w", err)
		}
		message.Coverage = &coverage
	}
	if answeredAt.Valid {
		value := answeredAt.Time.UTC().Format(time.RFC3339)
		message.AnsweredAt = &value
	}
	return message, nil
}

func normalizedIntent(value string) string {
	if strings.TrimSpace(value) == "" {
		return "knowledge_query"
	}
	return value
}

func scanKnowledgeFile(row scanner, file *domain.KnowledgeFile) error {
	var uploadedAt time.Time
	if err := row.Scan(&file.FileID, &file.Filename, &file.MIMEType, &file.SourceFormat, &file.Status, &file.UploadedBy, &uploadedAt, &file.ObjectURI, &file.ParseVersion, &file.IndexVersion); err != nil {
		return err
	}
	file.UploadedAt = uploadedAt.UTC().Format(time.RFC3339)
	return nil
}

func scanIngestionTask(row scanner, task *domain.IngestionTaskSummary) error {
	var requestedAt, updatedAt time.Time
	var errorCode, errorMessage sql.NullString
	var errorRetryable sql.NullBool
	if err := row.Scan(&task.TaskID, &task.FileID, &task.Status, &requestedAt, &updatedAt, &task.ForceReindex, &errorCode, &errorMessage, &errorRetryable); err != nil {
		return err
	}
	task.RequestedAt = requestedAt.UTC().Format(time.RFC3339)
	task.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if errorCode.Valid || errorMessage.Valid {
		task.Error = &domain.IngestionTaskError{
			Code:      errorCode.String,
			Message:   errorMessage.String,
			Retryable: errorRetryable.Valid && errorRetryable.Bool,
		}
	}
	return nil
}

func scanCitation(row scanner) (domain.Citation, error) {
	var item domain.Citation
	var locatorPayload []byte
	var documentID, revisionID, chunkSHA256, snapshotID sql.NullString
	if err := row.Scan(
		&item.MessageID, &item.ChunkID, &item.FileID, &documentID, &revisionID, &chunkSHA256, &snapshotID,
		&item.Filename, &item.Rank, &item.Score, &item.CitationLabel, &locatorPayload, &item.Snippet,
	); err != nil {
		return domain.Citation{}, err
	}
	if err := json.Unmarshal(locatorPayload, &item.SourceLocator); err != nil {
		return domain.Citation{}, err
	}
	item.DocumentID = nullableString(documentID)
	item.RevisionID = nullableString(revisionID)
	item.ChunkContentSHA256 = nullableString(chunkSHA256)
	item.KnowledgeSnapshotID = nullableString(snapshotID)
	return item, nil
}

func scanChunk(row scanner) (domain.ChunkRecord, error) {
	var item domain.ChunkRecord
	var pageNo sql.NullInt64
	var headingPayload, locatorPayload []byte
	if err := row.Scan(&item.ChunkID, &item.FileID, &item.SourceFormat, &item.BlockType, &headingPayload, &pageNo, &locatorPayload, &item.Text, &item.TokenCount); err != nil {
		return domain.ChunkRecord{}, err
	}
	if len(headingPayload) > 0 {
		_ = json.Unmarshal(headingPayload, &item.HeadingPath)
	}
	if pageNo.Valid {
		page := int(pageNo.Int64)
		item.PageNo = &page
	}
	if err := json.Unmarshal(locatorPayload, &item.SourceLocator); err != nil {
		return domain.ChunkRecord{}, err
	}
	return item, nil
}

func activeConfigTx(ctx context.Context, tx *sql.Tx) (domain.AgentConfig, error) {
	var config domain.AgentConfig
	row := tx.QueryRowContext(ctx, `
		SELECT config_version, model_id, system_prompt_version, response_style, temperature, top_p, max_output_tokens, COALESCE(answer_policy_version, 'legacy_unknown'), created_at
		FROM agent_configs
		ORDER BY created_at DESC, config_version DESC
		LIMIT 1`)
	err := scanAgentConfig(row, &config)
	return config, err
}

func activeQualityConfigPairTx(ctx context.Context, tx *sql.Tx) (domain.AgentConfig, domain.RetrievalConfig, error) {
	var agent domain.AgentConfig
	var retrieval domain.RetrievalConfig
	err := tx.QueryRowContext(ctx, `
		SELECT a.config_version, a.model_id, a.system_prompt_version, a.response_style, a.temperature, a.top_p, a.max_output_tokens, COALESCE(a.answer_policy_version, 'legacy_unknown'), a.created_at,
		 r.retrieval_config_version, r.retrieval_strategy, r.embedding_model, r.top_k, r.rerank_enabled, r.evidence_min_score, r.created_at
		FROM quality_active_release p
		JOIN quality_releases q ON q.release_id = p.release_id
		JOIN agent_configs a ON a.config_version = q.config_version
		JOIN retrieval_configs r ON r.retrieval_config_version = q.retrieval_config_version
		WHERE p.singleton = TRUE FOR SHARE`).Scan(
		&agent.ConfigVersion, &agent.ModelID, &agent.SystemPromptVersion, &agent.ResponseStyle, &agent.Temperature, &agent.TopP, &agent.MaxOutputTokens, &agent.AnswerPolicyVersion, &agent.CreatedAt,
		&retrieval.RetrievalConfigVersion, &retrieval.RetrievalStrategy, &retrieval.EmbeddingModel, &retrieval.TopK, &retrieval.RerankEnabled, &retrieval.EvidenceMinScore, &retrieval.CreatedAt,
	)
	if err != nil {
		return domain.AgentConfig{}, domain.RetrievalConfig{}, err
	}
	return agent, retrieval, nil
}

func activeRetrievalTx(ctx context.Context, tx *sql.Tx) (domain.RetrievalConfig, error) {
	var config domain.RetrievalConfig
	row := tx.QueryRowContext(ctx, `
		SELECT retrieval_config_version, retrieval_strategy, embedding_model, top_k, rerank_enabled, evidence_min_score, created_at
		FROM retrieval_configs
		ORDER BY created_at DESC, retrieval_config_version DESC
		LIMIT 1`)
	err := scanRetrievalConfig(row, &config)
	return config, err
}

func conversationForUpdateTx(ctx context.Context, tx *sql.Tx, id string) (domain.Conversation, error) {
	var conversation domain.Conversation
	row := tx.QueryRowContext(ctx, `
		SELECT conversation_id, owner_id, title, title_source, created_at, updated_at, config_version, retrieval_config_version, knowledge_snapshot_id, model_id
		FROM conversations
		WHERE conversation_id = $1
		FOR UPDATE`, id)
	err := scanConversation(row, &conversation)
	return conversation, err
}

func updateConversationTitleAndTimestampTx(ctx context.Context, tx *sql.Tx, conversation domain.Conversation) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET title = $2, title_source = $3, updated_at = $4
		WHERE conversation_id = $1`,
		conversation.ConversationID, conversation.Title, conversation.TitleSource, conversation.UpdatedAt,
	)
	return err
}

func fileByID(ctx context.Context, db *sql.DB, fileID string) (domain.KnowledgeFile, error) {
	var file domain.KnowledgeFile
	row := db.QueryRowContext(ctx, `
		SELECT file_id, filename, mime_type, source_format, status, uploaded_by, uploaded_at, object_uri, parse_version, index_version
		FROM knowledge_files
		WHERE file_id = $1`, fileID)
	err := scanKnowledgeFile(row, &file)
	return file, err
}

func fileByIDTx(ctx context.Context, tx *sql.Tx, fileID string) (domain.KnowledgeFile, error) {
	var file domain.KnowledgeFile
	row := tx.QueryRowContext(ctx, `
		SELECT file_id, filename, mime_type, source_format, status, uploaded_by, uploaded_at, object_uri, parse_version, index_version
		FROM knowledge_files
		WHERE file_id = $1
		FOR UPDATE`, fileID)
	err := scanKnowledgeFile(row, &file)
	return file, err
}

func latestTask(ctx context.Context, db *sql.DB, fileID string) (domain.IngestionTaskSummary, error) {
	var task domain.IngestionTaskSummary
	row := db.QueryRowContext(ctx, `
		SELECT task_id, file_id, status, requested_at, updated_at, force_reindex, error_code, error_message, error_retryable
		FROM ingestion_tasks
		WHERE file_id = $1
		ORDER BY requested_at DESC, task_id DESC
		LIMIT 1`, fileID)
	err := scanIngestionTask(row, &task)
	return task, err
}

func latestTaskTx(ctx context.Context, tx *sql.Tx, fileID string) (domain.IngestionTaskSummary, error) {
	var task domain.IngestionTaskSummary
	row := tx.QueryRowContext(ctx, `
		SELECT task_id, file_id, status, requested_at, updated_at, force_reindex, error_code, error_message, error_retryable
		FROM ingestion_tasks
		WHERE file_id = $1
		ORDER BY requested_at DESC, task_id DESC
		LIMIT 1
		FOR UPDATE`, fileID)
	err := scanIngestionTask(row, &task)
	return task, err
}

func (s *PostgresStore) listMessages(ctx context.Context, conversationID string) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, conversation_id, role, content, status, created_at, finish_reason, citations_count,
			knowledge_snapshot_id, knowledge_snapshot_manifest_hash, answered_at, freshness_status, intent, knowledge_used,
			answer_disposition, answer_policy_version, policy_evaluation_performed, answer_generation_performed, clarification_id, coverage
		FROM messages
		WHERE conversation_id = $1
		ORDER BY created_at ASC, message_id ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Message{}
	for rows.Next() {
		item, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	for index := range items {
		anchors, err := s.listCitationAnchors(ctx, items[index].MessageID)
		if err != nil {
			return nil, err
		}
		items[index].CitationAnchors = anchors
	}
	return items, nil
}

func nextCounterTx(ctx context.Context, tx *sql.Tx, name string) (int, error) {
	var next int
	err := tx.QueryRowContext(ctx, `
		UPDATE see_agent_counters
		SET next_value = next_value + 1
		WHERE name = $1
		RETURNING next_value - 1`, name).Scan(&next)
	return next, err
}

func nextCounter(ctx context.Context, db *sql.DB, name string) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	next, err := nextCounterTx(ctx, tx, name)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

func insertMessageTx(ctx context.Context, tx *sql.Tx, message domain.Message) error {
	var finishReason sql.NullString
	if message.FinishReason != nil {
		finishReason = sql.NullString{String: *message.FinishReason, Valid: true}
	}
	var coveragePayload any
	if message.Coverage != nil {
		payload, err := json.Marshal(message.Coverage)
		if err != nil {
			return err
		}
		coveragePayload = payload
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO messages (message_id, conversation_id, role, content, status, created_at, finish_reason, citations_count,
			knowledge_snapshot_id, knowledge_snapshot_manifest_hash, answered_at, freshness_status, intent, knowledge_used,
			answer_disposition, answer_policy_version, policy_evaluation_performed, answer_generation_performed, clarification_id, coverage)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, '')::timestamptz, NULLIF($12, ''), $13, $14,
			NULLIF($15, ''), NULLIF($16, ''), $17, $18, NULLIF($19, ''), NULLIF($20::jsonb, 'null'::jsonb))`,
		message.MessageID, message.ConversationID, message.Role, message.Content, message.Status, message.CreatedAt, finishReason, message.CitationsCount,
		message.KnowledgeSnapshotID, message.KnowledgeSnapshotManifestHash, message.AnsweredAt, message.FreshnessStatus, normalizedIntent(message.Intent), message.KnowledgeUsed,
		message.AnswerDisposition, message.AnswerPolicyVersion, message.PolicyEvaluationPerformed, message.AnswerGenerationPerformed, message.ClarificationID, coveragePayload)
	return err
}

func insertCitationTx(ctx context.Context, tx *sql.Tx, citation domain.Citation) error {
	payload, err := json.Marshal(citation.SourceLocator)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO citations (message_id, rank, chunk_id, file_id, document_id, revision_id, chunk_content_sha256, knowledge_snapshot_id,
			filename, score, citation_label, source_locator, snippet)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11, $12, $13)
		ON CONFLICT (message_id, rank, chunk_id) DO UPDATE SET
			file_id = EXCLUDED.file_id,
			document_id = EXCLUDED.document_id,
			revision_id = EXCLUDED.revision_id,
			chunk_content_sha256 = EXCLUDED.chunk_content_sha256,
			knowledge_snapshot_id = EXCLUDED.knowledge_snapshot_id,
			filename = EXCLUDED.filename,
			score = EXCLUDED.score,
			citation_label = EXCLUDED.citation_label,
			source_locator = EXCLUDED.source_locator,
			snippet = EXCLUDED.snippet`,
		citation.MessageID, citation.Rank, citation.ChunkID, citation.FileID, citation.DocumentID, citation.RevisionID, citation.ChunkContentSHA256, citation.KnowledgeSnapshotID,
		citation.Filename, citation.Score, citation.CitationLabel, payload, citation.Snippet)
	return err
}

func insertCitationAnchorsTx(ctx context.Context, tx *sql.Tx, messageID string, anchors []domain.CitationAnchor) error {
	for index, anchor := range anchors {
		ranks, err := json.Marshal(anchor.CitationRanks)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_citation_anchors (message_id, anchor_index, start_rune, end_rune, citation_ranks)
			VALUES ($1, $2, $3, $4, $5::jsonb)`,
			messageID, index, anchor.StartRune, anchor.EndRune, ranks,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) listCitationAnchors(ctx context.Context, messageID string) ([]domain.CitationAnchor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT start_rune, end_rune, citation_ranks
		FROM message_citation_anchors
		WHERE message_id = $1
		ORDER BY anchor_index ASC`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	anchors := []domain.CitationAnchor{}
	for rows.Next() {
		var anchor domain.CitationAnchor
		var ranks []byte
		if err := rows.Scan(&anchor.StartRune, &anchor.EndRune, &ranks); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ranks, &anchor.CitationRanks); err != nil {
			return nil, err
		}
		anchors = append(anchors, anchor)
	}
	return anchors, rows.Err()
}

func insertKnowledgeFileTx(ctx context.Context, tx *sql.Tx, file domain.KnowledgeFile) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_files (file_id, filename, mime_type, source_format, status, uploaded_by, uploaded_at, object_uri, parse_version, index_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (file_id) DO NOTHING`,
		file.FileID, file.Filename, file.MIMEType, file.SourceFormat, file.Status, file.UploadedBy, file.UploadedAt, file.ObjectURI, file.ParseVersion, file.IndexVersion)
	return err
}

func insertIngestionTaskTx(ctx context.Context, tx *sql.Tx, task domain.IngestionTaskSummary) error {
	var errorCode, errorMessage sql.NullString
	var errorRetryable sql.NullBool
	if task.Error != nil {
		errorCode = sql.NullString{String: task.Error.Code, Valid: true}
		errorMessage = sql.NullString{String: task.Error.Message, Valid: true}
		errorRetryable = sql.NullBool{Bool: task.Error.Retryable, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO ingestion_tasks (task_id, file_id, status, requested_at, updated_at, force_reindex, error_code, error_message, error_retryable)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (task_id) DO NOTHING`,
		task.TaskID, task.FileID, task.Status, task.RequestedAt, task.UpdatedAt, task.ForceReindex, errorCode, errorMessage, errorRetryable)
	return err
}

func replaceChunksTx(ctx context.Context, tx *sql.Tx, fileID string, chunks []domain.ChunkRecord) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE file_id = $1`, fileID); err != nil {
		return err
	}
	for _, chunk := range chunks {
		if err := insertChunkTx(ctx, tx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func insertChunkTx(ctx context.Context, tx *sql.Tx, chunk domain.ChunkRecord) error {
	locatorPayload, err := json.Marshal(chunk.SourceLocator)
	if err != nil {
		return err
	}
	headingPath := []string{}
	if chunk.HeadingPath != nil {
		headingPath = chunk.HeadingPath
	}
	headingPayload, err := json.Marshal(headingPath)
	if err != nil {
		return err
	}
	var pageNo sql.NullInt64
	if chunk.PageNo != nil {
		pageNo = sql.NullInt64{Int64: int64(*chunk.PageNo), Valid: true}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO chunks (chunk_id, file_id, source_format, block_type, heading_path, page_no, source_locator, text, token_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (chunk_id) DO UPDATE SET
			source_format = EXCLUDED.source_format,
			block_type = EXCLUDED.block_type,
			heading_path = EXCLUDED.heading_path,
			page_no = EXCLUDED.page_no,
			source_locator = EXCLUDED.source_locator,
			text = EXCLUDED.text,
			token_count = EXCLUDED.token_count`,
		chunk.ChunkID, chunk.FileID, chunk.SourceFormat, chunk.BlockType, headingPayload, pageNo, locatorPayload, chunk.Text, chunk.TokenCount)
	return err
}

var _ Store = (*PostgresStore)(nil)
