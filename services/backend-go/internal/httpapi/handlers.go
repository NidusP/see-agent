package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/auth"
	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
	"see-agent/services/backend-go/internal/service"
)

type Handlers struct {
	service        *service.Service
	auth           *auth.Manager
	readinessCheck func(context.Context) error
}

func New(svc *service.Service, managers ...*auth.Manager) *Handlers {
	var manager *auth.Manager
	if len(managers) > 0 {
		manager = managers[0]
	}
	return &Handlers{service: svc, auth: manager}
}

// WithReadinessCheck attaches the server-owned dependency probe without
// making HTTP handlers infer adapters from environment variables.
func (h *Handlers) WithReadinessCheck(check func(context.Context) error) *Handlers {
	h.readinessCheck = check
	return h
}

func (h *Handlers) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"service":   "backend",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handlers) Ready(w http.ResponseWriter, r *http.Request) {
	profile := strings.TrimSpace(os.Getenv("SEE_AGENT_PROFILE"))
	if profile == "fullstack_dev" || profile == "production" {
		for _, key := range []string{"DATABASE_URL", "REDIS_URL", "AI_RUNTIME_BASE_URL", "SEE_AGENT_RUNTIME_SERVICE_TOKEN"} {
			if strings.TrimSpace(os.Getenv(key)) == "" {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "service": "backend", "reason": "required dependency configuration is missing"})
				return
			}
		}
	}
	if h.readinessCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := h.readinessCheck(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "service": "backend", "reason": "a required dependency is unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "service": "backend"})
}

func (h *Handlers) AuthLogin(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeInfrastructureError(w, r, "auth_unavailable", "authentication service is unavailable")
		return
	}

	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}

	user, ok := h.auth.Authenticate(payload.Username, payload.Password)
	if !ok {
		WriteUnauthorized(w, r, "invalid_credentials", "invalid username or password")
		return
	}
	token, _, err := h.auth.CreateSession(user)
	if err != nil {
		writeInfrastructureError(w, r, "session_create_failed", "failed to create login session")
		return
	}
	auth.SetLoginCookies(w, token, user, h.auth.SessionMaxAgeSeconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

func (h *Handlers) AuthMe(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeInfrastructureError(w, r, "auth_unavailable", "authentication service is unavailable")
		return
	}
	user, ok := h.auth.UserFromRequest(r)
	if !ok {
		WriteUnauthorized(w, r, "unauthenticated", "login is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

func (h *Handlers) AuthLogout(w http.ResponseWriter, r *http.Request) {
	if h.auth != nil {
		if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
			h.auth.ClearSession(cookie.Value)
		}
	}
	auth.ClearLoginCookies(w)
	writeJSON(w, http.StatusOK, map[string]bool{
		"ok": true,
	})
}

func (h *Handlers) AgentConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"config": h.service.ActiveConfig(),
		})
	case http.MethodPut:
		var payload struct {
			ModelID             string   `json:"model_id"`
			SystemPromptVersion string   `json:"system_prompt_version"`
			ResponseStyle       *string  `json:"response_style"`
			AnswerPolicyVersion *string  `json:"answer_policy_version"`
			Temperature         float64  `json:"temperature"`
			TopP                *float64 `json:"top_p"`
			MaxOutputTokens     int      `json:"max_output_tokens"`
		}
		if !decodeJSON(w, r, &payload) {
			return
		}
		responseStyle := "concise"
		if payload.ResponseStyle != nil {
			responseStyle = strings.TrimSpace(*payload.ResponseStyle)
		}
		topP := 1.0
		if payload.TopP != nil {
			topP = *payload.TopP
		}
		answerPolicyVersion := ""
		if payload.AnswerPolicyVersion != nil {
			answerPolicyVersion = strings.TrimSpace(*payload.AnswerPolicyVersion)
		}
		if strings.TrimSpace(payload.ModelID) == "" || responseStyle == "" || payload.MaxOutputTokens < 1 || topP < 0 || topP > 1 {
			writeValidationError(w, r, "invalid_agent_config", "model_id, response_style, top_p, and max_output_tokens are required")
			return
		}
		config, err := h.service.UpdateConfig(payload.ModelID, payload.SystemPromptVersion, responseStyle, answerPolicyVersion, payload.Temperature, topP, payload.MaxOutputTokens)
		if errors.Is(err, service.ErrAgentConfigInvalid) {
			writeValidationError(w, r, "invalid_agent_config", "system_prompt_version and answer_policy_version must be a registered compatible pair")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"config": config,
		})
	default:
		writeMethodNotAllowed(w, r)
	}
}

func (h *Handlers) RetrievalConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"config": h.service.ActiveRetrievalConfig(),
		})
	case http.MethodPut:
		var payload struct {
			RetrievalStrategy string   `json:"retrieval_strategy"`
			EmbeddingModel    string   `json:"embedding_model"`
			TopK              int      `json:"top_k"`
			RerankEnabled     bool     `json:"rerank_enabled"`
			EvidenceMinScore  *float64 `json:"evidence_min_score"`
		}
		if !decodeJSON(w, r, &payload) {
			return
		}
		if !domain.IsRegisteredRetrievalStrategy(payload.RetrievalStrategy) || strings.TrimSpace(payload.EmbeddingModel) == "" || payload.TopK < 1 || payload.EvidenceMinScore == nil || *payload.EvidenceMinScore <= 0 || *payload.EvidenceMinScore > 1 {
			writeValidationError(w, r, "invalid_retrieval_config", "registered retrieval_strategy, embedding_model, top_k and evidence_min_score in (0,1] are required")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"config": h.service.UpdateRetrievalConfig(payload.RetrievalStrategy, payload.EmbeddingModel, payload.TopK, payload.RerankEnabled, *payload.EvidenceMinScore),
		})
	default:
		writeMethodNotAllowed(w, r)
	}
}

func (h *Handlers) ConfigVersions(w http.ResponseWriter, _ *http.Request) {
	items := h.service.ListConfigs()
	retrievalItems := h.service.ListRetrievalConfigs()
	writeJSON(w, http.StatusOK, map[string]any{
		"items":           items,
		"total":           len(items),
		"retrieval_items": retrievalItems,
		"retrieval_total": len(retrievalItems),
	})
}

func (h *Handlers) MessageExecutionEvidence(w http.ResponseWriter, r *http.Request) {
	item, found, err := h.service.GetMessageExecutionEvidence(r.Context(), r.PathValue("message_id"))
	if err != nil {
		writeInfrastructureError(w, r, "execution_evidence_unavailable", "execution evidence requires PostgreSQL")
		return
	}
	if !found {
		writeNotFound(w, r, "execution_evidence_not_found", "execution evidence was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"evidence": item})
}

func (h *Handlers) ExecutionEvidence(w http.ResponseWriter, r *http.Request) {
	limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeBadRequest(w, r, "invalid_evidence_filter", "limit must be between 1 and 100")
		return
	}
	page, err := h.service.ListMessageExecutionEvidence(r.Context(), r.URL.Query().Get("conversation_id"), r.URL.Query().Get("snapshot_id"), r.URL.Query().Get("revision_id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeInfrastructureError(w, r, "execution_evidence_unavailable", "execution evidence requires PostgreSQL")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handlers) EvalReports(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeBadRequest(w, r, "invalid_eval_filter", "limit must be between 1 and 100")
			return
		}
		page, err := h.service.ListEvalReports(r.Context(), r.URL.Query().Get("config_version"), r.URL.Query().Get("retrieval_config_version"), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			h.writeK7EvidenceError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		idempotencyKey, ok := requireIdempotencyKey(w, r)
		if !ok {
			return
		}
		var raw json.RawMessage
		if !decodeJSON(w, r, &raw) {
			return
		}
		var registration domain.EvalReportRegistration
		if err := json.Unmarshal(raw, &registration); err != nil {
			writeValidationError(w, r, "invalid_eval_report", "eval report must be a JSON object")
			return
		}
		registration.Report = raw
		report, err := h.service.RegisterEvalReport(r.Context(), idempotencyKey, registration)
		if err != nil {
			h.writeK7EvidenceError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"report": report})
	default:
		writeMethodNotAllowed(w, r)
	}
}

func (h *Handlers) EvalReportDetail(w http.ResponseWriter, r *http.Request) {
	report, found, err := h.service.GetEvalReport(r.Context(), r.PathValue("report_id"))
	if err != nil {
		h.writeK7EvidenceError(w, r, err)
		return
	}
	if !found {
		writeNotFound(w, r, "eval_report_not_found", "eval report was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": report})
}

func (h *Handlers) QualityFeedback(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeBadRequest(w, r, "invalid_feedback_filter", "limit must be between 1 and 100")
			return
		}
		items, err := h.service.ListAnswerFeedback(r.Context(), r.URL.Query().Get("message_id"), r.URL.Query().Get("kind"), limit)
		if err != nil {
			h.writeQualityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		key, ok := requireIdempotencyKey(w, r)
		if !ok {
			return
		}
		var payload struct {
			MessageID             string `json:"message_id"`
			Kind                  string `json:"kind"`
			Comment               string `json:"comment"`
			CorrectedAnswer       string `json:"corrected_answer"`
			ExpectedCitationRanks []int  `json:"expected_citation_ranks"`
		}
		if !decodeJSON(w, r, &payload) {
			return
		}
		item, err := h.service.CreateAnswerFeedback(r.Context(), h.lifecycleActorID(r), key, domain.AnswerFeedback{MessageID: payload.MessageID, Kind: payload.Kind, Comment: payload.Comment, CorrectedAnswer: payload.CorrectedAnswer, ExpectedCitationRanks: payload.ExpectedCitationRanks})
		if err != nil {
			h.writeQualityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"feedback": item})
	default:
		writeMethodNotAllowed(w, r)
	}
}
func (h *Handlers) QualityExperiments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeBadRequest(w, r, "invalid_experiment_filter", "limit must be between 1 and 100")
			return
		}
		page, err := h.service.ListQualityExperiments(r.Context(), r.URL.Query().Get("status"), limit)
		if err != nil {
			h.writeQualityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		key, ok := requireIdempotencyKey(w, r)
		if !ok {
			return
		}
		var payload struct {
			BaselineConfigVersion           string          `json:"baseline_config_version"`
			BaselineRetrievalConfigVersion  string          `json:"baseline_retrieval_config_version"`
			CandidateConfigVersion          string          `json:"candidate_config_version"`
			CandidateRetrievalConfigVersion string          `json:"candidate_retrieval_config_version"`
			KnowledgeSnapshotID             string          `json:"knowledge_snapshot_id"`
			KnowledgeSnapshotManifestHash   string          `json:"knowledge_snapshot_manifest_hash"`
			DatasetID                       string          `json:"dataset_id"`
			DatasetSHA256                   string          `json:"dataset_sha256"`
			Conditions                      json.RawMessage `json:"conditions"`
			Acceptance                      json.RawMessage `json:"acceptance"`
		}
		if !decodeJSON(w, r, &payload) {
			return
		}
		x, err := h.service.CreateQualityExperiment(r.Context(), h.lifecycleActorID(r), key, domain.CreateQualityExperimentRequest{BaselineConfigVersion: payload.BaselineConfigVersion, BaselineRetrievalConfigVersion: payload.BaselineRetrievalConfigVersion, CandidateConfigVersion: payload.CandidateConfigVersion, CandidateRetrievalConfigVersion: payload.CandidateRetrievalConfigVersion, KnowledgeSnapshotID: payload.KnowledgeSnapshotID, KnowledgeSnapshotManifestHash: payload.KnowledgeSnapshotManifestHash, DatasetID: payload.DatasetID, DatasetSHA256: payload.DatasetSHA256, Conditions: payload.Conditions, Acceptance: payload.Acceptance})
		if err != nil {
			h.writeQualityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"experiment": x})
	default:
		writeMethodNotAllowed(w, r)
	}
}
func (h *Handlers) QualityExperimentDetail(w http.ResponseWriter, r *http.Request) {
	x, found, err := h.service.GetQualityExperiment(r.Context(), r.PathValue("experiment_id"))
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	if !found {
		writeNotFound(w, r, "quality_experiment_not_found", "quality experiment was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"experiment": x})
}
func (h *Handlers) QualityExperimentFeedback(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var p struct {
		FeedbackID string `json:"feedback_id"`
	}
	if !decodeJSON(w, r, &p) {
		return
	}
	x, err := h.service.AttachQualityExperimentFeedback(r.Context(), h.lifecycleActorID(r), r.PathValue("experiment_id"), key, p.FeedbackID)
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"experiment": x})
}
func (h *Handlers) QualityExperimentFreeze(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	x, err := h.service.FreezeQualityExperiment(r.Context(), h.lifecycleActorID(r), r.PathValue("experiment_id"), key)
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"experiment": x})
}
func (h *Handlers) QualityExperimentReports(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var p struct {
		ReportID string `json:"report_id"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, &p) {
		return
	}
	x, err := h.service.RegisterQualityExperimentReport(r.Context(), h.lifecycleActorID(r), r.PathValue("experiment_id"), key, p.ReportID, p.Role)
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"experiment": x})
}
func (h *Handlers) QualityExperimentApprove(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var p struct {
		Reason string `json:"reason"`
	}
	if !decodeJSONAllowEmpty(w, r, &p) {
		return
	}
	x, err := h.service.ApproveQualityExperiment(r.Context(), h.lifecycleActorID(r), r.PathValue("experiment_id"), key, p.Reason)
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"experiment": x})
}
func (h *Handlers) QualityExperimentPublish(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	x, err := h.service.PublishQualityExperiment(r.Context(), h.lifecycleActorID(r), r.PathValue("experiment_id"), key)
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"release": x})
}
func (h *Handlers) QualityReleases(w http.ResponseWriter, r *http.Request) {
	limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeBadRequest(w, r, "invalid_release_filter", "limit must be between 1 and 100")
		return
	}
	page, err := h.service.ListQualityReleases(r.Context(), limit)
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (h *Handlers) QualityReleaseRollback(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	x, err := h.service.RollbackQualityRelease(r.Context(), h.lifecycleActorID(r), r.PathValue("release_id"), key)
	if err != nil {
		h.writeQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"release": x})
}
func (h *Handlers) OnboardingDatasets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeBadRequest(w, r, "invalid_onboarding_filter", "limit must be between 1 and 100")
			return
		}
		page, err := h.service.ListOnboardingDatasets(r.Context(), limit)
		if err != nil {
			h.writeOnboardingQualityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		key, ok := requireIdempotencyKey(w, r)
		if !ok {
			return
		}
		var payload struct {
			Name                string `json:"name"`
			KnowledgeSnapshotID string `json:"knowledge_snapshot_id"`
		}
		if !decodeJSON(w, r, &payload) {
			return
		}
		x, err := h.service.CreateOnboardingDataset(r.Context(), h.lifecycleActorID(r), key, payload.Name, payload.KnowledgeSnapshotID)
		if err != nil {
			h.writeOnboardingQualityError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"dataset": x})
	default:
		writeMethodNotAllowed(w, r)
	}
}
func (h *Handlers) OnboardingDatasetDetail(w http.ResponseWriter, r *http.Request) {
	x, found, err := h.service.GetOnboardingDataset(r.Context(), r.PathValue("dataset_id"))
	if err != nil {
		h.writeOnboardingQualityError(w, r, err)
		return
	}
	if !found {
		writeNotFound(w, r, "onboarding_dataset_not_found", "onboarding dataset was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataset": x})
}
func (h *Handlers) OnboardingDatasetCases(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var payload struct {
		CaseType             string   `json:"case_type"`
		Question             string   `json:"question"`
		ExpectedRevisionIDs  []string `json:"expected_revision_ids"`
		ExpectedLocatorHints []string `json:"expected_locator_hints"`
		ExpectedAnswerPoints []string `json:"expected_answer_points"`
		ReviewStatus         string   `json:"review_status"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	x, err := h.service.CreateOnboardingCase(r.Context(), h.lifecycleActorID(r), key, domain.OnboardingCase{DatasetID: r.PathValue("dataset_id"), CaseType: payload.CaseType, Question: payload.Question, ExpectedRevisionIDs: payload.ExpectedRevisionIDs, ExpectedLocatorHints: payload.ExpectedLocatorHints, ExpectedAnswerPoints: payload.ExpectedAnswerPoints, ReviewStatus: payload.ReviewStatus})
	if err != nil {
		h.writeOnboardingQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"case": x})
}
func (h *Handlers) OnboardingDatasetFreeze(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	x, err := h.service.FreezeOnboardingDataset(r.Context(), h.lifecycleActorID(r), r.PathValue("dataset_id"), key)
	if err != nil {
		h.writeOnboardingQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataset": x})
}
func (h *Handlers) OnboardingRuns(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	x, err := h.service.CreateOnboardingRun(r.Context(), h.lifecycleActorID(r), r.PathValue("dataset_id"), key)
	if err != nil {
		h.writeOnboardingQualityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": x})
}
func (h *Handlers) OnboardingRunDetail(w http.ResponseWriter, r *http.Request) {
	x, found, err := h.service.GetOnboardingRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		h.writeOnboardingQualityError(w, r, err)
		return
	}
	if !found {
		writeNotFound(w, r, "onboarding_run_not_found", "onboarding run was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": x})
}
func (h *Handlers) writeOnboardingQualityError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrOnboardingQualityUnavailable):
		writeRuntimeUnavailable(w, r, "onboarding_quality_unavailable", "onboarding quality requires PostgreSQL")
	case errors.Is(err, repository.ErrQualityNotFound):
		writeNotFound(w, r, "onboarding_quality_resource_not_found", "onboarding quality resource was not found")
	case errors.Is(err, service.ErrOnboardingQualityInvalid), errors.Is(err, repository.ErrQualityInvalid):
		writeValidationError(w, r, "invalid_onboarding_quality_operation", "onboarding quality request is invalid")
	case errors.Is(err, repository.ErrQualityConflict):
		writeConflict(w, r, "onboarding_quality_conflict", err.Error())
	default:
		writeInfrastructureError(w, r, "onboarding_quality_failed", "onboarding quality operation failed")
	}
}
func (h *Handlers) writeQualityError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrQualityOperationsUnavailable):
		writeRuntimeUnavailable(w, r, "quality_operations_unavailable", "quality operations require PostgreSQL")
	case errors.Is(err, repository.ErrQualityNotFound):
		writeNotFound(w, r, "quality_resource_not_found", "quality resource was not found")
	case errors.Is(err, service.ErrQualityOperationsInvalid), errors.Is(err, repository.ErrQualityInvalid):
		writeValidationError(w, r, "invalid_quality_operation", "quality request is invalid")
	case errors.Is(err, repository.ErrQualityConflict):
		writeConflict(w, r, "quality_operation_conflict", err.Error())
	default:
		writeInfrastructureError(w, r, "quality_operation_failed", "quality operation failed")
	}
}

func (h *Handlers) KnowledgeDeliveryEvidence(w http.ResponseWriter, r *http.Request) {
	limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeBadRequest(w, r, "invalid_delivery_filter", "limit must be between 1 and 100")
		return
	}
	page, err := h.service.ListKnowledgeDeliveryEvidence(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("document_id"), r.URL.Query().Get("revision_id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		h.writeK7EvidenceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handlers) writeK7EvidenceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrEvalReportInvalid):
		writeValidationError(w, r, "invalid_eval_report", "eval report metadata, configs, metrics and completed_at are required")
	case errors.Is(err, service.ErrDeliveryEvidenceFilter):
		writeBadRequest(w, r, "invalid_delivery_filter", "delivery status is invalid")
	case errors.Is(err, repository.ErrEvalReportConflict), errors.Is(err, repository.ErrIdempotencyKeyReused):
		writeConflict(w, r, "eval_report_conflict", "Idempotency-Key or report_id already represents different content")
	case errors.Is(err, service.ErrExecutionEvidenceUnavailable):
		writeInfrastructureError(w, r, "execution_evidence_unavailable", "K7 evidence requires PostgreSQL")
	default:
		writeInfrastructureError(w, r, "execution_evidence_failed", "K7 evidence operation failed")
	}
}

func (h *Handlers) Conversations(w http.ResponseWriter, r *http.Request) {
	ownerID := h.lifecycleActorID(r)
	switch r.Method {
	case http.MethodGet:
		items := h.service.ListConversationsAs(ownerID)
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"total": len(items),
		})
	case http.MethodPost:
		var payload struct {
			Title string `json:"title"`
		}
		if !decodeJSONAllowEmpty(w, r, &payload) {
			return
		}
		title := ""
		if strings.TrimSpace(payload.Title) != "" {
			var err error
			title, err = repository.NormalizeConversationTitle(payload.Title)
			if err != nil {
				writeValidationError(w, r, "validation_error", "title must contain 1 to 48 valid Unicode characters")
				return
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"conversation": h.service.CreateConversationAs(ownerID, title),
		})
	default:
		writeMethodNotAllowed(w, r)
	}
}

func (h *Handlers) ConversationDetail(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversation_id")
	conversation, messages, ok := h.service.GetConversationAs(h.lifecycleActorID(r), conversationID)
	if !ok {
		writeNotFound(w, r, "conversation_not_found", fmt.Sprintf("conversation %s was not found", conversationID))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": conversation,
		"messages":     messages,
	})
}

func (h *Handlers) RenameConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversation_id")
	var payload struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	conversation, found, err := h.service.RenameConversationAs(h.lifecycleActorID(r), conversationID, payload.Title)
	if errors.Is(err, repository.ErrInvalidConversationTitle) {
		writeValidationError(w, r, "validation_error", "title must contain 1 to 48 valid Unicode characters")
		return
	}
	if !found {
		writeNotFound(w, r, "conversation_not_found", "conversation was not found")
		return
	}
	if err != nil {
		writeInfrastructureError(w, r, "conversation_rename_failed", "failed to rename conversation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conversation})
}

func (h *Handlers) ConversationMessages(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversation_id")
	var payload struct {
		Content string `json:"content"`
		Stream  bool   `json:"stream"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.Content) == "" {
		writeValidationError(w, r, "invalid_message", "content is required")
		return
	}
	if wantsEventStream(r, payload.Stream) {
		h.streamConversationMessage(w, r, h.lifecycleActorID(r), conversationID, payload.Content)
		return
	}

	userMessage, assistantMessage, ok, err := h.service.CreateMessageAs(h.lifecycleActorID(r), conversationID, payload.Content, payload.Stream)
	if !ok {
		writeNotFound(w, r, "conversation_not_found", fmt.Sprintf("conversation %s was not found", conversationID))
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrKnowledgeSnapshotUnavailable) {
			writeConflict(w, r, "knowledge_snapshot_unavailable", "a published knowledge snapshot is not available yet")
			return
		}
		if errors.Is(err, service.ErrSnapshotScopeViolation) || errors.Is(err, service.ErrRuntimeCitationAnchorViolation) || errors.Is(err, service.ErrRuntimeExecutionFactViolation) {
			writeRuntimeContractViolation(w, r)
			return
		}
		if errors.Is(err, service.ErrRuntimeChatUnavailable) {
			writeRuntimeUnavailable(w, r, "runtime_unavailable", "runtime chat endpoint is unavailable")
			return
		}
		writeInfrastructureError(w, r, "chat_dispatch_failed", "failed to dispatch chat request")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_message":      userMessage,
		"assistant_message": assistantMessage,
	})
}

func (h *Handlers) streamConversationMessage(w http.ResponseWriter, r *http.Request, ownerID, conversationID, content string) {
	if !h.service.RuntimeStreamingEnabled() {
		writeRuntimeUnavailable(w, r, "runtime_unavailable", "runtime chat endpoint is unavailable")
		return
	}

	request, userMessage, streamingAssistant, ok, err := h.service.PrepareRuntimeStreamMessageAs(ownerID, conversationID, content)
	if !ok {
		writeNotFound(w, r, "conversation_not_found", fmt.Sprintf("conversation %s was not found", conversationID))
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrKnowledgeSnapshotUnavailable) {
			writeConflict(w, r, "knowledge_snapshot_unavailable", "a published knowledge snapshot is not available yet")
			return
		}
		if errors.Is(err, service.ErrSnapshotScopeViolation) || errors.Is(err, service.ErrRuntimeExecutionFactViolation) {
			writeRuntimeContractViolation(w, r)
			return
		}
		if errors.Is(err, service.ErrRuntimeChatUnavailable) {
			writeRuntimeUnavailable(w, r, "runtime_unavailable", "runtime chat endpoint is unavailable")
			return
		}
		writeInfrastructureError(w, r, "chat_dispatch_failed", "failed to dispatch chat request")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeInfrastructureError(w, r, "streaming_unsupported", "streaming is not supported by this response writer")
		return
	}

	streamRequestID := requestID(r)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	if !writeSSEEvent(w, "message.started", map[string]any{
		"request_id":           streamRequestID,
		"user_message":         userMessage,
		"assistant_message":    streamingAssistant,
		"conversation_id":      conversationID,
		"assistant_message_id": streamingAssistant.MessageID,
	}) {
		return
	}
	flusher.Flush()

	var runtimeResponse *domain.ChatRuntimeResponse
	runtimeErrorSent := false
	err = h.service.StreamRuntimeChat(r.Context(), request, func(event domain.ChatRuntimeStreamEvent) error {
		switch event.Event {
		case "message.delta":
			if !writeSSEEvent(w, "message.delta", map[string]any{
				"request_id": streamRequestID,
				"message_id": streamingAssistant.MessageID,
				"delta":      event.Delta,
				"status":     "streaming",
			}) {
				return fmt.Errorf("write stream delta")
			}
			flusher.Flush()
		case "message.completed":
			if event.Response != nil {
				runtimeResponse = event.Response
			}
		case "message.error":
			h.service.FailRuntimeStreamAttemptForEvent(r.Context(), request, event.Error)
			runtimeErrorSent = true
			writeRuntimeStreamError(w, streamRequestID, streamingAssistant.MessageID, event.Error)
			flusher.Flush()
			return fmt.Errorf("runtime stream error")
		}
		return nil
	})
	if err != nil {
		if !runtimeErrorSent {
			h.service.FailRuntimeStreamAttemptForError(r.Context(), request, err)
			writeRuntimeStreamError(w, streamRequestID, streamingAssistant.MessageID, &domain.ChatRuntimeStreamError{
				Code:      "runtime_unavailable",
				Message:   "runtime chat stream endpoint is unavailable",
				Retryable: true,
			})
			flusher.Flush()
		}
		return
	}
	if runtimeResponse == nil {
		h.service.FailRuntimeStreamAttempt(r.Context(), request, "runtime_contract_violation", "runtime_response_validation", true)
		writeRuntimeStreamError(w, streamRequestID, streamingAssistant.MessageID, &domain.ChatRuntimeStreamError{
			Code:      "runtime_stream_incomplete",
			Message:   "runtime stream completed without a final response",
			Retryable: true,
		})
		flusher.Flush()
		return
	}
	finalUserMessage, finalAssistantMessage, ok, completionErr := h.service.CompleteRuntimeStreamMessage(request, userMessage.MessageID, content, *runtimeResponse)
	if completionErr != nil {
		code, stage := "dependency_unavailable", "state_writeback"
		if errors.Is(completionErr, service.ErrRuntimeCitationAnchorViolation) || errors.Is(completionErr, service.ErrRuntimeExecutionFactViolation) || errors.Is(completionErr, service.ErrSnapshotScopeViolation) {
			code, stage = "runtime_contract_violation", "runtime_response_validation"
		}
		h.service.FailRuntimeStreamAttempt(r.Context(), request, code, stage, true)
		streamError := &domain.ChatRuntimeStreamError{
			Code:      "state_writeback_failed",
			Message:   "failed to persist runtime stream response",
			Retryable: false,
		}
		if errors.Is(completionErr, service.ErrSnapshotScopeViolation) {
			streamError = &domain.ChatRuntimeStreamError{
				Code:      "runtime_contract_violation",
				Message:   "runtime response did not satisfy the execution contract",
				Retryable: true,
			}
		}
		if errors.Is(completionErr, service.ErrRuntimeCitationAnchorViolation) || errors.Is(completionErr, service.ErrRuntimeExecutionFactViolation) {
			streamError = &domain.ChatRuntimeStreamError{
				Code:      "runtime_contract_violation",
				Message:   "runtime response did not satisfy the execution contract",
				Retryable: true,
			}
		}
		writeRuntimeStreamError(w, streamRequestID, streamingAssistant.MessageID, streamError)
		flusher.Flush()
		return
	}
	if !ok {
		writeRuntimeStreamError(w, streamRequestID, streamingAssistant.MessageID, &domain.ChatRuntimeStreamError{
			Code:      "state_writeback_failed",
			Message:   "failed to persist runtime stream response",
			Retryable: false,
		})
		flusher.Flush()
		return
	}

	if !writeSSEEvent(w, "message.completed", map[string]any{
		"request_id":           streamRequestID,
		"user_message":         finalUserMessage,
		"assistant_message":    finalAssistantMessage,
		"conversation_id":      conversationID,
		"assistant_message_id": finalAssistantMessage.MessageID,
	}) {
		return
	}
	flusher.Flush()
}

func (h *Handlers) MessageSources(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversation_id")
	ownerID := h.lifecycleActorID(r)
	if _, _, ok := h.service.GetConversationAs(ownerID, conversationID); !ok {
		writeNotFound(w, r, "conversation_not_found", "conversation was not found")
		return
	}
	messageID := r.PathValue("message_id")
	sources, ok := h.service.GetMessageSourcesAs(ownerID, conversationID, messageID)
	if !ok {
		writeNotFound(w, r, "conversation_not_found", "conversation or message was not found")
		return
	}
	writeJSON(w, http.StatusOK, sources)
}

func (h *Handlers) RevalidateMessage(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var payload struct {
		Reason string `json:"reason"`
	}
	if !decodeJSONAllowEmpty(w, r, &payload) {
		return
	}
	if len(payload.Reason) > 500 {
		writeValidationError(w, r, "revalidation_invalid", "reason must not exceed 500 characters")
		return
	}
	run, err := h.service.CreateRevalidationAs(r.Context(), h.lifecycleActorID(r), r.PathValue("conversation_id"), r.PathValue("message_id"), key, payload.Reason)
	if err != nil {
		if errors.Is(err, service.ErrConversationNotFound) {
			writeNotFound(w, r, "conversation_not_found", "conversation was not found")
			return
		}
		h.writeKnowledgeFreshnessError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"revalidation": run})
}

func (h *Handlers) RevalidationDetail(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversation_id")
	ownerID := h.lifecycleActorID(r)
	if _, _, ok := h.service.GetConversationAs(ownerID, conversationID); !ok {
		writeNotFound(w, r, "conversation_not_found", "conversation was not found")
		return
	}
	run, found, err := h.service.GetRevalidationAs(r.Context(), ownerID, conversationID, r.PathValue("revalidation_id"))
	if err != nil {
		h.writeKnowledgeFreshnessError(w, r, err)
		return
	}
	if !found {
		writeNotFound(w, r, "revalidation_not_found", "revalidation run was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revalidation": run})
}

func (h *Handlers) KnowledgeFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status := r.URL.Query().Get("status")
		sourceFormat := r.URL.Query().Get("source_format")
		if !service.ValidFileStatus(status) {
			writeValidationError(w, r, "invalid_status", "unsupported status filter")
			return
		}
		if !service.ValidSourceFormat(sourceFormat) {
			writeValidationError(w, r, "invalid_source_format", "unsupported source_format filter")
			return
		}

		items := h.service.ListFiles(status, sourceFormat)
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"total": len(items),
		})
	case http.MethodPost:
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeValidationError(w, r, "invalid_multipart", "failed to parse multipart form")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeValidationError(w, r, "missing_file", "multipart field `file` is required")
			return
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			writeInfrastructureError(w, r, "upload_read_failed", "failed to read uploaded file")
			return
		}
		forceReindex := r.FormValue("force_reindex") == "true"
		createdFile, task, err := h.service.CreateLegacyFileAs(r.Context(), h.lifecycleActorID(r), header.Filename, header.Header.Get("Content-Type"), content, forceReindex)
		if err != nil {
			if errors.Is(err, service.ErrIngestionQueueUnavailable) {
				writeInfrastructureError(w, r, "queue_unavailable", "failed to enqueue ingestion task")
				return
			}
			writeInfrastructureError(w, r, "upload_persist_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"file":           createdFile,
			"ingestion_task": task,
		})
	default:
		writeMethodNotAllowed(w, r)
	}
}

func (h *Handlers) KnowledgeDocuments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeValidationError(w, r, "invalid_limit", err.Error())
			return
		}
		page, err := h.service.ListKnowledgeDocuments(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("next_cursor"), limit)
		if err != nil {
			h.writeKnowledgeLifecycleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		key, ok := requireIdempotencyKey(w, r)
		if !ok {
			return
		}
		filename, mimeType, content, ok := parseKnowledgeUpload(w, r)
		if !ok {
			return
		}
		result, err := h.service.CreateKnowledgeDocument(r.Context(), service.KnowledgeDocumentUploadInput{
			ActorID: h.lifecycleActorID(r), IdempotencyKey: key, Filename: filename, MIMEType: mimeType, Content: content,
			DisplayName: r.FormValue("display_name"), EffectiveAt: r.FormValue("effective_at"),
		})
		if err != nil {
			h.writeKnowledgeLifecycleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	default:
		writeMethodNotAllowed(w, r)
	}
}

func (h *Handlers) KnowledgeUpdates(w http.ResponseWriter, r *http.Request) {
	limit, err := parseKnowledgeLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeValidationError(w, r, "invalid_limit", err.Error())
		return
	}
	page, err := h.service.ListKnowledgeUpdates(r.Context(), r.URL.Query().Get("document_id"), r.URL.Query().Get("next_cursor"), limit)
	if err != nil {
		h.writeKnowledgeFreshnessError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handlers) KnowledgeSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, found, err := h.service.GetKnowledgeSnapshot(r.Context(), r.PathValue("snapshot_id"))
	if err != nil {
		h.writeKnowledgeFreshnessError(w, r, err)
		return
	}
	if !found {
		writeNotFound(w, r, "snapshot_not_found", "knowledge snapshot was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": snapshot})
}

func (h *Handlers) KnowledgeDocumentDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := h.service.GetKnowledgeDocument(r.Context(), r.PathValue("document_id"))
	if err != nil {
		h.writeKnowledgeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handlers) KnowledgeRetirementReadiness(w http.ResponseWriter, r *http.Request) {
	readiness, err := h.service.GetKnowledgeRetirementReadiness(r.Context(), r.PathValue("document_id"))
	if err != nil {
		h.writeKnowledgeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"readiness": readiness})
}

func (h *Handlers) CreateKnowledgeRevision(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	filename, mimeType, content, ok := parseKnowledgeUpload(w, r)
	if !ok {
		return
	}
	result, err := h.service.CreateKnowledgeRevision(r.Context(), service.KnowledgeRevisionUploadInput{
		ActorID: h.lifecycleActorID(r), IdempotencyKey: key, DocumentID: r.PathValue("document_id"), Filename: filename,
		MIMEType: mimeType, Content: content, EffectiveAt: r.FormValue("effective_at"),
	})
	if err != nil {
		h.writeKnowledgeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (h *Handlers) PublishKnowledgeRevision(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.service.PublishKnowledgeRevision(r.Context(), h.lifecycleActorID(r), r.PathValue("revision_id"), key)
	if err != nil {
		h.writeKnowledgeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) DisableKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.service.DisableKnowledgeDocument(r.Context(), h.lifecycleActorID(r), r.PathValue("document_id"), key)
	if err != nil {
		h.writeKnowledgeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) DeleteKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := h.service.DeleteKnowledgeDocument(r.Context(), h.lifecycleActorID(r), r.PathValue("document_id"), key)
	if err != nil {
		h.writeKnowledgeLifecycleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeBadRequest(w, r, "missing_idempotency_key", "Idempotency-Key header is required")
		return "", false
	}
	if len(key) > 200 {
		writeBadRequest(w, r, "invalid_idempotency_key", "Idempotency-Key must not exceed 200 characters")
		return "", false
	}
	return key, true
}

func parseKnowledgeUpload(w http.ResponseWriter, r *http.Request) (string, string, []byte, bool) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeValidationError(w, r, "invalid_upload", "failed to parse multipart upload")
		return "", "", nil, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeValidationError(w, r, "invalid_upload", "multipart field `file` is required")
		return "", "", nil, false
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		writeInfrastructureError(w, r, "upload_read_failed", "failed to read uploaded file")
		return "", "", nil, false
	}
	return header.Filename, header.Header.Get("Content-Type"), content, true
}

func parseKnowledgeLimit(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 20, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("limit must be an integer between 1 and 100")
	}
	return limit, nil
}

func (h *Handlers) lifecycleActorID(r *http.Request) string {
	if h.auth != nil {
		if user, ok := h.auth.UserFromRequest(r); ok {
			return user.UserID
		}
	}
	return "admin"
}

func (h *Handlers) writeKnowledgeLifecycleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeLifecycleUnavailable):
		writeRuntimeUnavailable(w, r, "knowledge_lifecycle_unavailable", "knowledge lifecycle requires PostgreSQL")
	case errors.Is(err, service.ErrInvalidKnowledgeUpload):
		writeValidationError(w, r, "invalid_upload", err.Error())
	case errors.Is(err, repository.ErrIdempotencyKeyReused):
		writeConflict(w, r, "idempotency_key_reused", "Idempotency-Key was previously used with a different request")
	case errors.Is(err, repository.ErrKnowledgeDocumentNotFound):
		writeNotFound(w, r, "document_not_found", "knowledge document was not found")
	case errors.Is(err, repository.ErrRevisionNotFound):
		writeNotFound(w, r, "revision_not_found", "knowledge revision was not found")
	case errors.Is(err, repository.ErrKnowledgeDocumentNotActive):
		writeConflict(w, r, "document_not_active", "knowledge document is not active")
	case errors.Is(err, repository.ErrDocumentHasInflightRevision):
		writeConflict(w, r, "document_has_inflight_revision", "knowledge document has a revision still being indexed")
	case errors.Is(err, repository.ErrKnowledgeDocumentNotDisabled):
		writeConflict(w, r, "document_not_disabled", "knowledge document must be disabled before delete")
	case errors.Is(err, repository.ErrKnowledgeRetirementNotReady):
		writeConflict(w, r, "retirement_not_ready", err.Error())
	case errors.Is(err, repository.ErrRevisionNotReady), errors.Is(err, repository.ErrRevisionManifestMismatch), errors.Is(err, repository.ErrRevisionNotEffective):
		writeConflict(w, r, "revision_not_ready", "knowledge revision is not ready to publish")
	case errors.Is(err, repository.ErrKnowledgeDocumentConflict), errors.Is(err, repository.ErrKnowledgeRevisionConflict):
		writeConflict(w, r, "knowledge_conflict", err.Error())
	default:
		writeInfrastructureError(w, r, "knowledge_lifecycle_failed", "knowledge lifecycle operation failed")
	}
}

func (h *Handlers) writeKnowledgeFreshnessError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeFreshnessUnavailable):
		writeRuntimeUnavailable(w, r, "knowledge_freshness_unavailable", "knowledge freshness requires PostgreSQL")
	case errors.Is(err, service.ErrKnowledgeSnapshotUnavailable), errors.Is(err, repository.ErrActiveSnapshotUnavailable):
		writeConflict(w, r, "knowledge_snapshot_unavailable", "a published knowledge snapshot is not available yet")
	case errors.Is(err, service.ErrSnapshotScopeViolation), errors.Is(err, repository.ErrKnowledgeSnapshotCitationMismatch):
		writeConflict(w, r, "snapshot_scope_violation", "runtime response did not match the selected knowledge snapshot")
	case errors.Is(err, repository.ErrIdempotencyKeyReused):
		writeConflict(w, r, "idempotency_key_reused", "Idempotency-Key was previously used with a different request")
	case errors.Is(err, repository.ErrRevalidationInvalid):
		writeConflict(w, r, "revalidation_invalid", "the historical answer cannot be revalidated")
	case errors.Is(err, repository.ErrRevalidationNotFound):
		writeNotFound(w, r, "revalidation_not_found", "revalidation run was not found")
	default:
		writeInfrastructureError(w, r, "knowledge_freshness_failed", "knowledge freshness operation failed")
	}
}

func (h *Handlers) KnowledgeFileDetail(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	file, latestTask, ok := h.service.GetFile(fileID)
	if !ok {
		writeNotFound(w, r, "file_not_found", fmt.Sprintf("file %s was not found", fileID))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":        file,
		"latest_task": latestTask,
	})
}

func (h *Handlers) ReindexKnowledgeFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	var payload struct {
		ForceReindex bool `json:"force_reindex"`
	}
	if !decodeJSONAllowEmpty(w, r, &payload) {
		return
	}
	if !payload.ForceReindex {
		payload.ForceReindex = true
	}
	task, ok, err := h.service.ReindexFile(fileID, payload.ForceReindex)
	if !ok {
		writeNotFound(w, r, "file_not_found", fmt.Sprintf("file %s was not found", fileID))
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrIngestionQueueUnavailable) {
			writeInfrastructureError(w, r, "queue_unavailable", "failed to enqueue ingestion task")
			return
		}
		writeInfrastructureError(w, r, "reindex_dispatch_failed", "failed to dispatch reindex task")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ingestion_task": task,
	})
}

func (h *Handlers) KnowledgeFileChunks(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	items, ok := h.service.ListChunks(fileID)
	if !ok {
		writeNotFound(w, r, "file_not_found", fmt.Sprintf("file %s was not found", fileID))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func writeValidationError(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func writeBadRequest(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func writeConflict(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func WriteUnauthorized(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func WriteForbidden(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func writeNotFound(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
		"error": map[string]string{
			"code":       "method_not_allowed",
			"message":    "method not allowed",
			"request_id": requestID(r),
		},
	})
}

func writeInfrastructureError(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func writeRuntimeUnavailable(w http.ResponseWriter, r *http.Request, code, message string) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r),
		},
	})
}

func writeRuntimeContractViolation(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusBadGateway, map[string]any{
		"error": map[string]any{
			"code":       "runtime_contract_violation",
			"message":    "runtime response did not satisfy the execution contract",
			"retryable":  true,
			"stage":      "runtime_response_validation",
			"request_id": requestID(r),
		},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		writeValidationError(w, r, "missing_body", "request body is required")
		return false
	}
	return decodeJSONAllowEmpty(w, r, dst)
}

func decodeJSONAllowEmpty(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if err.Error() == "EOF" {
			return true
		}
		writeValidationError(w, r, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func wantsEventStream(r *http.Request, stream bool) bool {
	return stream || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

func writeSSEEvent(w io.Writer, event string, payload any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded); err != nil {
		return false
	}
	return true
}

func writeRuntimeStreamError(w io.Writer, requestID, messageID string, streamError *domain.ChatRuntimeStreamError) bool {
	if streamError == nil {
		streamError = &domain.ChatRuntimeStreamError{
			Code:      "runtime_unavailable",
			Message:   "runtime chat stream endpoint is unavailable",
			Retryable: true,
		}
	}
	return writeSSEEvent(w, "message.error", map[string]any{
		"request_id": requestID,
		"message_id": messageID,
		"error":      streamError,
	})
}

func splitSSEContentDeltas(content string, chunkSize int) []string {
	if chunkSize < 1 {
		chunkSize = 24
	}
	runes := []rune(content)
	if len(runes) == 0 {
		return []string{}
	}
	deltas := make([]string, 0, (len(runes)+chunkSize-1)/chunkSize)
	for start := 0; start < len(runes); start += chunkSize {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		deltas = append(deltas, string(runes[start:end]))
	}
	return deltas
}

func requestID(r *http.Request) string {
	return fmt.Sprintf("req_%d", time.Now().UTC().UnixNano())
}
