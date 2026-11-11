package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/auth"
	"see-agent/services/backend-go/internal/httpapi"
	"see-agent/services/backend-go/internal/objectstore"
	"see-agent/services/backend-go/internal/repository"
	runtimeclient "see-agent/services/backend-go/internal/runtime"
	"see-agent/services/backend-go/internal/service"
	"see-agent/services/backend-go/internal/taskqueue"
)

type Server struct {
	httpServer     *http.Server
	stopK3Delivery func()
	startupSummary []string
}

func New(addr string) *Server {
	if err := validateRuntimeProfile(); err != nil {
		panic(err)
	}
	store := buildStore()
	runtimeBaseURL := os.Getenv("AI_RUNTIME_BASE_URL")
	uploadRoot := os.Getenv("BACKEND_UPLOAD_DIR")
	runtimeTimeout, err := configuredRuntimeChatTimeout()
	if err != nil {
		panic(err)
	}
	runtimeHTTPClient := runtimeclient.NewClient(runtimeBaseURL, runtimeTimeout)
	chatRuntimeEnabled := strings.TrimSpace(runtimeBaseURL) != ""
	uploadStore := objectstore.FromEnv(uploadRoot)
	ingestionQueue := buildIngestionQueue()
	svc := service.NewWithDependencies(store, runtimeHTTPClient, uploadStore, ingestionQueue, chatRuntimeEnabled)
	svc.SetRuntimeCallTimeout(runtimeTimeout)
	stopK3Delivery := startK3Delivery(store, runtimeHTTPClient, runtimeBaseURL)
	authManager := buildAuthManager()
	handlers := httpapi.New(svc, authManager).WithReadinessCheck(buildReadinessChecker(store, ingestionQueue, runtimeBaseURL))
	startupSummary := buildStartupSummary(store)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handlers.Health)
	mux.HandleFunc("GET /readyz", handlers.Ready)
	mux.HandleFunc("POST /api/auth/login", handlers.AuthLogin)
	mux.HandleFunc("GET /api/auth/me", requireUser(authManager, handlers.AuthMe))
	mux.HandleFunc("POST /api/auth/logout", handlers.AuthLogout)
	mux.HandleFunc("GET /api/agent/config", requireSystemAdmin(authManager, handlers.AgentConfig))
	mux.HandleFunc("PUT /api/agent/config", requireSystemAdmin(authManager, handlers.AgentConfig))
	mux.HandleFunc("GET /api/agent/retrieval-config", requireSystemAdmin(authManager, handlers.RetrievalConfig))
	mux.HandleFunc("PUT /api/agent/retrieval-config", requireSystemAdmin(authManager, handlers.RetrievalConfig))
	mux.HandleFunc("GET /api/agent/config/versions", requireSystemAdmin(authManager, handlers.ConfigVersions))
	mux.HandleFunc("GET /api/admin/evidence/executions", requireSystemAdmin(authManager, handlers.ExecutionEvidence))
	mux.HandleFunc("GET /api/admin/evidence/messages/{message_id}", requireSystemAdmin(authManager, handlers.MessageExecutionEvidence))
	mux.HandleFunc("GET /api/admin/evidence/deliveries", requireSystemAdmin(authManager, handlers.KnowledgeDeliveryEvidence))
	mux.HandleFunc("GET /api/eval/reports", requireSystemAdmin(authManager, handlers.EvalReports))
	mux.HandleFunc("POST /api/eval/reports", requireSystemAdmin(authManager, handlers.EvalReports))
	mux.HandleFunc("GET /api/eval/reports/{report_id}", requireSystemAdmin(authManager, handlers.EvalReportDetail))
	mux.HandleFunc("GET /api/admin/quality/feedback", requireSystemAdmin(authManager, handlers.QualityFeedback))
	mux.HandleFunc("POST /api/admin/quality/feedback", requireSystemAdmin(authManager, handlers.QualityFeedback))
	mux.HandleFunc("GET /api/admin/quality/experiments", requireSystemAdmin(authManager, handlers.QualityExperiments))
	mux.HandleFunc("POST /api/admin/quality/experiments", requireSystemAdmin(authManager, handlers.QualityExperiments))
	mux.HandleFunc("GET /api/admin/quality/experiments/{experiment_id}", requireSystemAdmin(authManager, handlers.QualityExperimentDetail))
	mux.HandleFunc("POST /api/admin/quality/experiments/{experiment_id}/feedback", requireSystemAdmin(authManager, handlers.QualityExperimentFeedback))
	mux.HandleFunc("POST /api/admin/quality/experiments/{experiment_id}/freeze", requireSystemAdmin(authManager, handlers.QualityExperimentFreeze))
	mux.HandleFunc("POST /api/admin/quality/experiments/{experiment_id}/reports", requireSystemAdmin(authManager, handlers.QualityExperimentReports))
	mux.HandleFunc("POST /api/admin/quality/experiments/{experiment_id}/approve", requireSystemAdmin(authManager, handlers.QualityExperimentApprove))
	mux.HandleFunc("POST /api/admin/quality/experiments/{experiment_id}/publish", requireSystemAdmin(authManager, handlers.QualityExperimentPublish))
	mux.HandleFunc("GET /api/admin/quality/releases", requireSystemAdmin(authManager, handlers.QualityReleases))
	mux.HandleFunc("POST /api/admin/quality/releases/{release_id}/rollback", requireSystemAdmin(authManager, handlers.QualityReleaseRollback))
	mux.HandleFunc("GET /api/admin/onboarding/datasets", requireKnowledgeAdmin(authManager, handlers.OnboardingDatasets))
	mux.HandleFunc("POST /api/admin/onboarding/datasets", requireKnowledgeAdmin(authManager, handlers.OnboardingDatasets))
	mux.HandleFunc("GET /api/admin/onboarding/datasets/{dataset_id}", requireKnowledgeAdmin(authManager, handlers.OnboardingDatasetDetail))
	mux.HandleFunc("POST /api/admin/onboarding/datasets/{dataset_id}/cases", requireKnowledgeAdmin(authManager, handlers.OnboardingDatasetCases))
	mux.HandleFunc("POST /api/admin/onboarding/datasets/{dataset_id}/freeze", requireKnowledgeAdmin(authManager, handlers.OnboardingDatasetFreeze))
	mux.HandleFunc("POST /api/admin/onboarding/datasets/{dataset_id}/runs", requireKnowledgeAdmin(authManager, handlers.OnboardingRuns))
	mux.HandleFunc("GET /api/admin/onboarding/runs/{run_id}", requireKnowledgeAdmin(authManager, handlers.OnboardingRunDetail))
	mux.HandleFunc("GET /api/chat/conversations", requireUser(authManager, handlers.Conversations))
	mux.HandleFunc("POST /api/chat/conversations", requireUser(authManager, handlers.Conversations))
	mux.HandleFunc("GET /api/chat/conversations/{conversation_id}", requireUser(authManager, handlers.ConversationDetail))
	mux.HandleFunc("PATCH /api/chat/conversations/{conversation_id}", requireUser(authManager, handlers.RenameConversation))
	mux.HandleFunc("POST /api/chat/conversations/{conversation_id}/messages", requireUser(authManager, handlers.ConversationMessages))
	mux.HandleFunc("GET /api/chat/conversations/{conversation_id}/sources/{message_id}", requireUser(authManager, handlers.MessageSources))
	mux.HandleFunc("POST /api/chat/conversations/{conversation_id}/messages/{message_id}/revalidate", requireUser(authManager, handlers.RevalidateMessage))
	mux.HandleFunc("GET /api/chat/conversations/{conversation_id}/revalidations/{revalidation_id}", requireUser(authManager, handlers.RevalidationDetail))
	mux.HandleFunc("GET /api/kb/files", requireKnowledgeAdmin(authManager, handlers.KnowledgeFiles))
	mux.HandleFunc("POST /api/kb/files", requireKnowledgeAdmin(authManager, handlers.KnowledgeFiles))
	mux.HandleFunc("GET /api/kb/files/{file_id}", requireKnowledgeAdmin(authManager, handlers.KnowledgeFileDetail))
	mux.HandleFunc("POST /api/kb/files/{file_id}/reindex", requireKnowledgeAdmin(authManager, handlers.ReindexKnowledgeFile))
	mux.HandleFunc("GET /api/kb/files/{file_id}/chunks", requireKnowledgeAdmin(authManager, handlers.KnowledgeFileChunks))
	mux.HandleFunc("GET /api/kb/documents", requireKnowledgeAdmin(authManager, handlers.KnowledgeDocuments))
	mux.HandleFunc("POST /api/kb/documents", requireKnowledgeAdmin(authManager, handlers.KnowledgeDocuments))
	mux.HandleFunc("GET /api/kb/documents/{document_id}", requireKnowledgeAdmin(authManager, handlers.KnowledgeDocumentDetail))
	mux.HandleFunc("GET /api/kb/documents/{document_id}/retirement-readiness", requireSystemAdmin(authManager, handlers.KnowledgeRetirementReadiness))
	mux.HandleFunc("POST /api/kb/documents/{document_id}/revisions", requireKnowledgeAdmin(authManager, handlers.CreateKnowledgeRevision))
	mux.HandleFunc("POST /api/kb/revisions/{revision_id}/publish", requireSystemAdmin(authManager, handlers.PublishKnowledgeRevision))
	mux.HandleFunc("POST /api/kb/documents/{document_id}/disable", requireSystemAdmin(authManager, handlers.DisableKnowledgeDocument))
	mux.HandleFunc("DELETE /api/kb/documents/{document_id}", requireSystemAdmin(authManager, handlers.DeleteKnowledgeDocument))
	mux.HandleFunc("GET /api/kb/updates", requireKnowledgeAdmin(authManager, handlers.KnowledgeUpdates))
	mux.HandleFunc("GET /api/kb/snapshots/{snapshot_id}", requireKnowledgeAdmin(authManager, handlers.KnowledgeSnapshot))

	return &Server{
		stopK3Delivery: stopK3Delivery,
		startupSummary: startupSummary,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

const defaultRuntimeChatTimeout = 60 * time.Second

func configuredRuntimeChatTimeout() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS"))
	if raw == "" {
		return defaultRuntimeChatTimeout, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 5 || seconds > 600 {
		return 0, fmt.Errorf("BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS must be an integer from 5 to 600")
	}
	return time.Duration(seconds) * time.Second, nil
}

func validateRuntimeProfile() error {
	profile := strings.TrimSpace(os.Getenv("SEE_AGENT_PROFILE"))
	if profile == "" || profile == "test" || profile == "smoke" {
		return nil
	}
	if profile != "fullstack_dev" && profile != "production" {
		return fmt.Errorf("SEE_AGENT_PROFILE must be test, smoke, fullstack_dev, or production")
	}
	if _, err := configuredRuntimeChatTimeout(); err != nil {
		return err
	}
	if err := validateRuntimeServiceTokenPolicy(); err != nil {
		return err
	}
	for _, key := range []string{
		"DATABASE_URL", "REDIS_URL", "AI_RUNTIME_BASE_URL", "SEE_AGENT_RUNTIME_SERVICE_TOKEN",
		"OBJECT_STORE_ENDPOINT", "OBJECT_STORE_BUCKET", "OBJECT_STORE_ACCESS_KEY", "OBJECT_STORE_SECRET_KEY",
		"SEE_AGENT_SYSTEM_ADMIN_USERNAME", "SEE_AGENT_SYSTEM_ADMIN_PASSWORD",
		"SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME", "SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD",
		"SEE_AGENT_USER_USERNAME", "SEE_AGENT_USER_PASSWORD",
	} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			return fmt.Errorf("%s requires %s", profile, key)
		}
	}
	if strings.TrimSpace(os.Getenv("AUTH_SESSION_STORE")) != "redis" {
		return fmt.Errorf("%s requires AUTH_SESSION_STORE=redis", profile)
	}
	if !envBool("K3_DELIVERY_ENABLED", false) {
		return fmt.Errorf("%s requires K3_DELIVERY_ENABLED=true", profile)
	}
	return nil
}

const runtimeServiceTokenPlaceholder = "replace-with-local-dev-secret"

func validateRuntimeServiceTokenPolicy() error {
	current := strings.TrimSpace(os.Getenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN"))
	if err := validateRuntimeServiceToken("SEE_AGENT_RUNTIME_SERVICE_TOKEN", current); err != nil {
		return err
	}

	previous := strings.TrimSpace(os.Getenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS"))
	if previous == "" {
		return nil
	}
	if err := validateRuntimeServiceToken("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", previous); err != nil {
		return err
	}
	if previous == current {
		return fmt.Errorf("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS must differ from SEE_AGENT_RUNTIME_SERVICE_TOKEN")
	}
	return nil
}

func validateRuntimeServiceToken(name, token string) error {
	if token == "" {
		return fmt.Errorf("%s must be configured with a non-empty secret", name)
	}
	if token == runtimeServiceTokenPlaceholder {
		return fmt.Errorf("%s must not use the repository placeholder", name)
	}
	if len(token) < 32 {
		return fmt.Errorf("%s must be at least 32 characters", name)
	}
	return nil
}

func buildReadinessChecker(store repository.Store, queue taskqueue.Queue, runtimeBaseURL string) func(context.Context) error {
	return func(ctx context.Context) error {
		profile := strings.TrimSpace(os.Getenv("SEE_AGENT_PROFILE"))
		if profile != "fullstack_dev" && profile != "production" {
			return nil
		}
		healthStore, ok := store.(repository.HealthStore)
		if !ok {
			return fmt.Errorf("strict profile store does not support health probing")
		}
		if err := healthStore.Ping(ctx); err != nil {
			return fmt.Errorf("postgres readiness probe failed: %w", err)
		}
		healthQueue, ok := queue.(taskqueue.HealthQueue)
		if !ok {
			return fmt.Errorf("strict profile queue does not support health probing")
		}
		if err := healthQueue.Ping(ctx); err != nil {
			return fmt.Errorf("redis readiness probe failed: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(runtimeBaseURL, "/")+"/readyz", nil)
		if err != nil {
			return fmt.Errorf("create runtime readiness probe: %w", err)
		}
		response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
		if err != nil {
			return fmt.Errorf("runtime readiness probe failed: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("runtime readiness probe returned %d", response.StatusCode)
		}
		return nil
	}
}

func buildStore() repository.Store {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return repository.NewMemoryStore()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := repository.NewPostgresStore(ctx, databaseURL)
	if err != nil {
		panic(err)
	}
	return store
}

func buildIngestionQueue() taskqueue.Queue {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return taskqueue.NewLocalQueue(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queue, err := taskqueue.NewRedisQueue(ctx, redisURL, os.Getenv("REDIS_INGESTION_QUEUE"))
	if err != nil {
		panic(err)
	}
	return queue
}

func startK3Delivery(store repository.Store, runtimeHTTPClient *runtimeclient.Client, runtimeBaseURL string) func() {
	if !envBool("K3_DELIVERY_ENABLED", false) {
		return nil
	}
	knowledgeStore, ok := store.(repository.KnowledgeDeliveryStore)
	if !ok {
		panic("K3_DELIVERY_ENABLED requires DATABASE_URL backed by Postgres")
	}
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		panic("K3_DELIVERY_ENABLED requires REDIS_URL")
	}
	if strings.TrimSpace(runtimeBaseURL) == "" {
		panic("K3_DELIVERY_ENABLED requires AI_RUNTIME_BASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	consumerName := strings.TrimSpace(os.Getenv("REDIS_K3_REVISION_CONSUMER"))
	if consumerName == "" {
		consumerName = "backend-" + strconv.Itoa(os.Getpid())
	}
	stream, err := taskqueue.NewRedisRevisionStream(
		ctx,
		redisURL,
		os.Getenv("REDIS_K3_REVISION_STREAM"),
		os.Getenv("REDIS_K3_REVISION_GROUP"),
		consumerName,
	)
	if err != nil {
		panic(err)
	}
	runtimeAdapter, err := service.NewRevisionRuntimeAdapter(runtimeHTTPClient)
	if err != nil {
		_ = stream.Close()
		panic(err)
	}
	stop, err := service.StartRevisionDeliveryPipeline(context.Background(), knowledgeStore, stream, runtimeAdapter, k3RelayInterval())
	if err != nil {
		_ = stream.Close()
		panic(err)
	}
	return stop
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		panic(name + " must be true or false")
	}
	return parsed
}

func k3RelayInterval() time.Duration {
	value := strings.TrimSpace(os.Getenv("K3_DELIVERY_RELAY_INTERVAL_MS"))
	if value == "" {
		return time.Second
	}
	milliseconds, err := strconv.Atoi(value)
	if err != nil || milliseconds < 50 {
		panic("K3_DELIVERY_RELAY_INTERVAL_MS must be an integer of at least 50")
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func buildAuthManager() *auth.Manager {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_SESSION_STORE")))
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if mode == "" && redisURL != "" {
		mode = "redis"
	}
	if mode != "redis" {
		return auth.NewDefaultManager()
	}
	if redisURL == "" {
		panic("AUTH_SESSION_STORE=redis requires REDIS_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionStore, err := auth.NewRedisSessionStore(ctx, redisURL, os.Getenv("REDIS_SESSION_PREFIX"))
	if err != nil {
		panic(err)
	}
	return auth.NewManagerWithSessionStore(auth.DefaultCredentialsFromEnv(), auth.DefaultSessionTTL(), sessionStore)
}

func (s *Server) ListenAndServe() error {
	err := s.httpServer.ListenAndServe()
	if s.stopK3Delivery != nil {
		s.stopK3Delivery()
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.stopK3Delivery != nil {
		s.stopK3Delivery()
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

func (s *Server) StartupSummary() []string {
	return append([]string(nil), s.startupSummary...)
}

func requireUser(manager *auth.Manager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := manager.UserFromRequest(r); !ok {
			httpapi.WriteUnauthorized(w, r, "unauthenticated", "login is required")
			return
		}
		next(w, r)
	}
}

func requireKnowledgeAdmin(manager *auth.Manager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := manager.UserFromRequest(r)
		if !ok {
			httpapi.WriteUnauthorized(w, r, "unauthenticated", "login is required")
			return
		}
		if !auth.IsKnowledgeAdminRole(user.Role) {
			httpapi.WriteForbidden(w, r, "knowledge_admin_required", "knowledge administrator role is required")
			return
		}
		next(w, r)
	}
}

func requireSystemAdmin(manager *auth.Manager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := manager.UserFromRequest(r)
		if !ok {
			httpapi.WriteUnauthorized(w, r, "unauthenticated", "login is required")
			return
		}
		if !auth.IsSystemAdminRole(user.Role) {
			httpapi.WriteForbidden(w, r, "system_admin_required", "system administrator role is required")
			return
		}
		next(w, r)
	}
}
