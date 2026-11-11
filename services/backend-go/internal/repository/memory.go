package repository

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

type MemoryStore struct {
	mu sync.RWMutex

	nextConversation int
	nextMessage      int
	nextFile         int
	nextTask         int
	nextConfig       int
	nextRetrieval    int

	conversations map[string]domain.Conversation
	messages      map[string][]domain.Message
	citations     map[string][]domain.Citation
	files         map[string]domain.KnowledgeFile
	latestTasks   map[string]domain.IngestionTaskSummary
	chunks        map[string][]domain.ChunkRecord
	configs       []domain.AgentConfig
	retrievals    []domain.RetrievalConfig
	attempts      map[string]domain.ChatExecutionAttempt
}

func NewMemoryStore() *MemoryStore {
	store := &MemoryStore{
		nextConversation: 1,
		nextMessage:      1,
		nextFile:         2,
		nextTask:         2,
		nextConfig:       2,
		nextRetrieval:    2,
		conversations:    make(map[string]domain.Conversation),
		messages:         make(map[string][]domain.Message),
		citations:        make(map[string][]domain.Citation),
		files:            make(map[string]domain.KnowledgeFile),
		latestTasks:      make(map[string]domain.IngestionTaskSummary),
		chunks:           make(map[string][]domain.ChunkRecord),
		attempts:         make(map[string]domain.ChatExecutionAttempt),
	}

	now := time.Now().UTC().Format(time.RFC3339)
	store.configs = []domain.AgentConfig{{
		ConfigVersion:       "cfg_001",
		ModelID:             defaultAgentModelID(),
		SystemPromptVersion: "prompt_v7",
		ResponseStyle:       "concise",
		Temperature:         0,
		TopP:                1,
		MaxOutputTokens:     1024,
		AnswerPolicyVersion: domain.LegacyAnswerPolicyVersion,
		CreatedAt:           now,
	}}
	store.retrievals = []domain.RetrievalConfig{{
		RetrievalConfigVersion: "rcfg_001",
		RetrievalStrategy:      domain.RetrievalStrategyNativeDenseV1,
		EmbeddingModel:         "qwen3-embedding:0.6b",
		TopK:                   3,
		RerankEnabled:          false,
		EvidenceMinScore:       0.51,
		CreatedAt:              now,
	}}

	pageOne := 1
	seedFile := domain.KnowledgeFile{
		FileID:       "file_001",
		Filename:     "employee-handbook.txt",
		MIMEType:     "text/plain",
		SourceFormat: "txt",
		Status:       "succeeded",
		UploadedBy:   "admin",
		UploadedAt:   now,
		ObjectURI:    "minio://knowledge/file_001/employee-handbook.txt",
		ParseVersion: "parse_v1",
		IndexVersion: "index_v1",
	}
	seedTask := domain.IngestionTaskSummary{
		TaskID:      "task_001",
		FileID:      "file_001",
		Status:      "succeeded",
		RequestedAt: now,
		UpdatedAt:   now,
	}
	seedChunk := domain.ChunkRecord{
		ChunkID:      "chunk_001",
		FileID:       "file_001",
		SourceFormat: "txt",
		BlockType:    "paragraph",
		SourceLocator: domain.SourceLocator{
			SourceFormat: "txt",
			PageNo:       &pageOne,
			BlockLabel:   "paragraph-1",
		},
		Text:       "Employees must submit reimbursement claims within 10 calendar days after travel ends.",
		TokenCount: 14,
	}

	store.files[seedFile.FileID] = seedFile
	store.latestTasks[seedFile.FileID] = seedTask
	store.chunks[seedFile.FileID] = []domain.ChunkRecord{seedChunk}

	return store
}

func (s *MemoryStore) ActiveConfig() domain.AgentConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configs[len(s.configs)-1]
}

func (s *MemoryStore) GetConfig(configVersion string) (domain.AgentConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, config := range s.configs {
		if config.ConfigVersion == configVersion {
			return config, true
		}
	}
	return domain.AgentConfig{}, false
}

func (s *MemoryStore) CreateConfig(modelID, promptVersion, responseStyle, answerPolicyVersion string, temperature, topP float64, maxOutputTokens int) domain.AgentConfig {
	s.mu.Lock()
	defer s.mu.Unlock()

	answerPolicyVersion = strings.TrimSpace(answerPolicyVersion)
	if answerPolicyVersion == "" {
		answerPolicyVersion = domain.LegacyAnswerPolicyVersion
	}
	config := domain.AgentConfig{
		ConfigVersion:       fmt.Sprintf("cfg_%03d", s.nextConfig),
		ModelID:             modelID,
		SystemPromptVersion: promptVersion,
		ResponseStyle:       responseStyle,
		Temperature:         temperature,
		TopP:                topP,
		MaxOutputTokens:     maxOutputTokens,
		AnswerPolicyVersion: answerPolicyVersion,
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
	}
	s.nextConfig++
	s.configs = append(s.configs, config)
	return config
}

func (s *MemoryStore) ListConfigs() []domain.AgentConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.configs)
}

func (s *MemoryStore) ActiveRetrievalConfig() domain.RetrievalConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retrievals[len(s.retrievals)-1]
}

func (s *MemoryStore) GetRetrievalConfig(retrievalConfigVersion string) (domain.RetrievalConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, config := range s.retrievals {
		if config.RetrievalConfigVersion == retrievalConfigVersion {
			return config, true
		}
	}
	return domain.RetrievalConfig{}, false
}

func (s *MemoryStore) CreateRetrievalConfig(retrievalStrategy, embeddingModel string, topK int, rerankEnabled bool, evidenceMinScore float64) domain.RetrievalConfig {
	s.mu.Lock()
	defer s.mu.Unlock()

	config := domain.RetrievalConfig{
		RetrievalConfigVersion: fmt.Sprintf("rcfg_%03d", s.nextRetrieval),
		RetrievalStrategy:      retrievalStrategy,
		EmbeddingModel:         embeddingModel,
		TopK:                   topK,
		RerankEnabled:          rerankEnabled,
		EvidenceMinScore:       evidenceMinScore,
		CreatedAt:              time.Now().UTC().Format(time.RFC3339),
	}
	s.nextRetrieval++
	s.retrievals = append(s.retrievals, config)
	return config
}

func (s *MemoryStore) ListRetrievalConfigs() []domain.RetrievalConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.retrievals)
}

func (s *MemoryStore) CreateConversation(title string) domain.Conversation {
	return s.CreateOwnedConversation("user_admin", title)
}

func (s *MemoryStore) CreateOwnedConversation(ownerID, title string) domain.Conversation {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return domain.Conversation{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	titleSource := ConversationTitleUserEdited
	if strings.TrimSpace(title) == "" {
		title = DefaultConversationTitle
		titleSource = ConversationTitleSystemDefault
	}

	activeConfig := s.configs[len(s.configs)-1]
	activeRetrieval := s.retrievals[len(s.retrievals)-1]
	conversation := domain.Conversation{
		ConversationID:         fmt.Sprintf("conv_%03d", s.nextConversation),
		OwnerID:                ownerID,
		Title:                  title,
		TitleSource:            titleSource,
		CreatedAt:              now,
		UpdatedAt:              now,
		ConfigVersion:          activeConfig.ConfigVersion,
		RetrievalConfigVersion: activeRetrieval.RetrievalConfigVersion,
		KnowledgeSnapshotID:    "snapshot_active",
		ModelID:                activeConfig.ModelID,
	}
	s.nextConversation++
	s.conversations[conversation.ConversationID] = conversation
	return conversation
}

func (s *MemoryStore) ListOwnedConversations(ownerID string) []domain.Conversation {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return []domain.Conversation{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]domain.Conversation, 0, len(s.conversations))
	for _, item := range s.conversations {
		if item.OwnerID == ownerID {
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(a, b domain.Conversation) int {
		return strings.Compare(b.UpdatedAt, a.UpdatedAt)
	})
	return items
}

func (s *MemoryStore) ListConversations() []domain.Conversation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]domain.Conversation, 0, len(s.conversations))
	for _, item := range s.conversations {
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b domain.Conversation) int {
		return strings.Compare(b.UpdatedAt, a.UpdatedAt)
	})
	return items
}

func (s *MemoryStore) GetConversation(id string) (domain.Conversation, []domain.Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conversation, ok := s.conversations[id]
	if !ok {
		return domain.Conversation{}, nil, false
	}
	messages := slices.Clone(s.messages[id])
	if messages == nil {
		messages = []domain.Message{}
	}
	return conversation, messages, true
}

func (s *MemoryStore) GetOwnedConversation(ownerID, conversationID string) (domain.Conversation, []domain.Message, bool) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return domain.Conversation{}, nil, false
	}
	conversation, messages, ok := s.GetConversation(conversationID)
	if !ok || conversation.OwnerID != ownerID {
		return domain.Conversation{}, nil, false
	}
	return conversation, messages, true
}

func (s *MemoryStore) RenameOwnedConversation(ownerID, conversationID, title string) (domain.Conversation, bool) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return domain.Conversation{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.conversations[conversationID]
	if !ok || conversation.OwnerID != ownerID {
		return domain.Conversation{}, false
	}
	conversation.Title = title
	conversation.TitleSource = ConversationTitleUserEdited
	conversation.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.conversations[conversationID] = conversation
	return conversation, true
}

func (s *MemoryStore) ReserveMessageID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	messageID := fmt.Sprintf("msg_%03d", s.nextMessage)
	s.nextMessage++
	return messageID
}

func (s *MemoryStore) AppendRuntimeExchange(conversationID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, bool) {
	return s.AppendRuntimeExchangeWithIDs(conversationID, "", content, result)
}

func (s *MemoryStore) AppendRuntimeExchangeWithIDs(conversationID, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	conversation, ok := s.conversations[conversationID]
	if !ok {
		return domain.Message{}, domain.Message{}, nil, false
	}

	now := time.Now().UTC().Format(time.RFC3339)
	finishReason := strings.TrimSpace(result.FinishReason)
	assistantContent := strings.TrimSpace(result.AssistantMessage.Content)
	if assistantContent == "" || finishReason == "" || strings.TrimSpace(result.MessageID) == "" {
		return domain.Message{}, domain.Message{}, nil, false
	}

	userMessageID = strings.TrimSpace(userMessageID)
	if userMessageID == "" {
		userMessageID = fmt.Sprintf("msg_%03d", s.nextMessage)
		s.nextMessage++
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

	assistantMessageID := strings.TrimSpace(result.MessageID)
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
		FreshnessStatus: "unknown",
		CitationAnchors: slices.Clone(result.CitationAnchors),
		Intent:          normalizedIntent(result.Intent),
		KnowledgeUsed:   result.KnowledgeUsed,
	}
	applyA1MessageProjection(&assistantMessage, result)

	s.messages[conversationID] = append(s.messages[conversationID], userMessage, assistantMessage)
	if len(citations) > 0 {
		s.citations[assistantMessage.MessageID] = citations
	}
	applyAutomaticConversationTitle(&conversation, content)
	conversation.UpdatedAt = now
	s.conversations[conversationID] = conversation

	return userMessage, assistantMessage, citations, true
}

func (s *MemoryStore) GetSources(messageID string) []domain.Citation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.citations[messageID])
}

func (s *MemoryStore) GetMessageSources(messageID string) (domain.MessageSources, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := slices.Clone(s.citations[messageID])
	if len(items) == 0 {
		return domain.MessageSources{}, false
	}
	return domain.MessageSources{
		MessageID:       messageID,
		FreshnessStatus: "unknown",
		Items:           items,
	}, true
}

func (s *MemoryStore) ListFiles(status, sourceFormat string) []domain.KnowledgeFile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]domain.KnowledgeFile, 0, len(s.files))
	for _, item := range s.files {
		if status != "" && item.Status != status {
			continue
		}
		if sourceFormat != "" && item.SourceFormat != sourceFormat {
			continue
		}
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b domain.KnowledgeFile) int {
		return strings.Compare(b.UploadedAt, a.UploadedAt)
	})
	return items
}

func (s *MemoryStore) ReserveFileID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	fileID := fmt.Sprintf("file_%03d", s.nextFile)
	s.nextFile++
	return fileID
}

func (s *MemoryStore) CreateFile(fileID, filename, providedMIMEType, objectURI string, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(fileID) == "" {
		fileID = fmt.Sprintf("file_%03d", s.nextFile)
		s.nextFile++
	}
	taskID := fmt.Sprintf("task_%03d", s.nextTask)
	s.nextTask++

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
		TaskID:       taskID,
		FileID:       fileID,
		Status:       "queued",
		RequestedAt:  now,
		UpdatedAt:    now,
		ForceReindex: forceReindex,
	}

	s.files[fileID] = file
	s.latestTasks[fileID] = task
	s.chunks[fileID] = []domain.ChunkRecord{{
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
	}}

	return file, task
}

func (s *MemoryStore) MarkTaskRunning(fileID, taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, ok := s.files[fileID]
	if !ok {
		return false
	}
	task, ok := s.latestTasks[fileID]
	if !ok || task.TaskID != taskID {
		return false
	}

	now := time.Now().UTC().Format(time.RFC3339)
	file.Status = "running"
	task.Status = "running"
	task.UpdatedAt = now
	task.Error = nil
	s.files[fileID] = file
	s.latestTasks[fileID] = task
	return true
}

func (s *MemoryStore) GetFile(fileID string) (domain.KnowledgeFile, domain.IngestionTaskSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	file, ok := s.files[fileID]
	if !ok {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}
	return file, s.latestTasks[fileID], true
}

func (s *MemoryStore) ReindexFile(fileID string, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, ok := s.files[fileID]
	if !ok {
		return domain.KnowledgeFile{}, domain.IngestionTaskSummary{}, false
	}

	now := time.Now().UTC().Format(time.RFC3339)
	task := domain.IngestionTaskSummary{
		TaskID:       fmt.Sprintf("task_%03d", s.nextTask),
		FileID:       fileID,
		Status:       "queued",
		RequestedAt:  now,
		UpdatedAt:    now,
		ForceReindex: forceReindex,
	}
	s.nextTask++
	file.Status = "queued"
	file.ParseVersion = nextVersion(file.ParseVersion, "parse")
	file.IndexVersion = nextVersion(file.IndexVersion, "index")
	s.files[fileID] = file
	s.latestTasks[fileID] = task
	s.chunks[fileID] = []domain.ChunkRecord{{
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
	}}

	return file, task, true
}

func (s *MemoryStore) ListChunks(fileID string) ([]domain.ChunkRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	chunks, ok := s.chunks[fileID]
	if !ok {
		return nil, false
	}
	return slices.Clone(chunks), true
}

func (s *MemoryStore) ApplyIngestionResult(fileID, taskID string, result domain.IngestionTaskResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, ok := s.files[fileID]
	if !ok {
		return false
	}
	task, ok := s.latestTasks[fileID]
	if !ok || task.TaskID != taskID {
		return false
	}

	status := result.Status
	if status == "" {
		status = "failed"
	}

	task.Status = status
	task.UpdatedAt = coalesceTimestamp(result.UpdatedAt)
	task.Error = result.Error
	file.Status = status
	s.files[fileID] = file
	s.latestTasks[fileID] = task

	switch status {
	case "succeeded":
		s.chunks[fileID] = buildSucceededChunks(file, result)
	case "failed":
		s.chunks[fileID] = buildFailedChunks(file, result.Error)
	}
	return true
}

func detectFileShape(filename string) (string, string) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".md":
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
		return "txt", "text/plain"
	}
}

func nextVersion(current, prefix string) string {
	value := strings.TrimSpace(current)
	if value == "" {
		return prefix + "_v1"
	}

	lastUnderscore := strings.LastIndex(value, "_")
	if lastUnderscore == -1 || lastUnderscore == len(value)-1 {
		return prefix + "_v1"
	}

	lastSegment := value[lastUnderscore+1:]
	if len(lastSegment) < 2 || lastSegment[0] != 'v' {
		return prefix + "_v1"
	}

	version, err := strconv.Atoi(lastSegment[1:])
	if err != nil || version < 1 {
		return prefix + "_v1"
	}
	return fmt.Sprintf("%s_v%d", prefix, version+1)
}

func buildSucceededChunks(file domain.KnowledgeFile, result domain.IngestionTaskResult) []domain.ChunkRecord {
	pageNo := 1
	text := fmt.Sprintf("Runtime ingestion succeeded for %s and reported %d chunks created.", file.Filename, result.ChunksCreated)
	return []domain.ChunkRecord{{
		ChunkID:      fmt.Sprintf("chunk_%s_summary", file.FileID),
		FileID:       file.FileID,
		SourceFormat: file.SourceFormat,
		BlockType:    "ingestion_summary",
		PageNo:       &pageNo,
		SourceLocator: domain.SourceLocator{
			SourceFormat: file.SourceFormat,
			PageNo:       &pageNo,
			BlockLabel:   "runtime-summary",
		},
		Text:       text,
		TokenCount: len(strings.Fields(text)),
	}}
}

func buildFailedChunks(file domain.KnowledgeFile, taskErr *domain.IngestionTaskError) []domain.ChunkRecord {
	message := fmt.Sprintf("Runtime ingestion failed for %s.", file.Filename)
	if taskErr != nil && strings.TrimSpace(taskErr.Message) != "" {
		message = fmt.Sprintf("Runtime ingestion failed for %s: %s", file.Filename, taskErr.Message)
	}
	return []domain.ChunkRecord{{
		ChunkID:      fmt.Sprintf("chunk_%s_failed", file.FileID),
		FileID:       file.FileID,
		SourceFormat: file.SourceFormat,
		BlockType:    "ingestion_error",
		SourceLocator: domain.SourceLocator{
			SourceFormat: file.SourceFormat,
			BlockLabel:   "runtime-error",
		},
		Text:       message,
		TokenCount: len(strings.Fields(message)),
	}}
}

func coalesceTimestamp(value string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return time.Now().UTC().Format(time.RFC3339)
}
