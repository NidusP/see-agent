package server

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"see-agent/services/backend-go/internal/repository"
)

func buildStartupSummary(store repository.Store) []string {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	runtimeURL := strings.TrimSpace(os.Getenv("AI_RUNTIME_BASE_URL"))
	objectEndpoint := strings.TrimSpace(os.Getenv("OBJECT_STORE_ENDPOINT"))
	objectBucket := strings.TrimSpace(os.Getenv("OBJECT_STORE_BUCKET"))

	repositorySummary := "repository=memory status=ephemeral"
	if databaseURL != "" {
		repositorySummary = "repository=postgres " + summarizeDatabaseURL(databaseURL) + " status=initialized"
	}

	queueSummary := "ingestion_queue=local status=ephemeral"
	if redisURL != "" {
		queueSummary = "ingestion_queue=redis " + summarizeRedisURL(redisURL) + " status=initialized"
	}

	sessionMode := strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_SESSION_STORE")))
	if sessionMode == "" && redisURL != "" {
		sessionMode = "redis"
	}
	if sessionMode != "redis" {
		sessionMode = "memory"
	}
	sessionSummary := "session_store=" + sessionMode
	if sessionMode == "redis" {
		sessionSummary += " " + summarizeRedisURL(redisURL) + " status=initialized"
	} else {
		sessionSummary += " status=ephemeral"
	}

	runtimeSummary := "ai_runtime=disabled endpoint=none"
	if runtimeURL != "" {
		runtimeSummary = "ai_runtime=configured endpoint=" + sanitizeURL(runtimeURL)
	}

	objectSummary := "object_store=local status=ephemeral"
	if objectEndpoint != "" && objectBucket != "" {
		objectSummary = fmt.Sprintf(
			"object_store=s3_compatible endpoint=%s bucket=%s status=configured",
			sanitizeURL(objectEndpoint),
			objectBucket,
		)
	}

	activeConfig := store.ActiveConfig()
	agentSummary := fmt.Sprintf(
		"active_agent_config version=%s model=%s prompt=%s",
		valueOrUnset(activeConfig.ConfigVersion),
		valueOrUnset(activeConfig.ModelID),
		valueOrUnset(activeConfig.SystemPromptVersion),
	)
	activeRetrieval := store.ActiveRetrievalConfig()
	retrievalSummary := fmt.Sprintf(
		"active_retrieval_config version=%s strategy=%s embedding_model=%s",
		valueOrUnset(activeRetrieval.RetrievalConfigVersion),
		valueOrUnset(activeRetrieval.RetrievalStrategy),
		valueOrUnset(activeRetrieval.EmbeddingModel),
	)

	return []string{
		repositorySummary,
		queueSummary,
		sessionSummary,
		runtimeSummary,
		objectSummary,
		fmt.Sprintf("k3_delivery enabled=%t", envBool("K3_DELIVERY_ENABLED", false)),
		agentSummary,
		retrievalSummary,
	}
}

func summarizeDatabaseURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "endpoint=invalid database=unknown"
	}
	return fmt.Sprintf("endpoint=%s database=%s", sanitizedHost(parsed), valueOrUnset(strings.TrimPrefix(parsed.Path, "/")))
}

func summarizeRedisURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "endpoint=invalid database=unknown"
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if database == "" {
		database = "0"
	}
	return fmt.Sprintf("endpoint=%s database=%s", sanitizedHost(parsed), database)
}

func sanitizeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "invalid"
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	return parsed.Scheme + "://" + sanitizedHost(parsed) + path
}

func sanitizedHost(parsed *url.URL) string {
	host := parsed.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	return host
}

func valueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unset"
	}
	return strings.TrimSpace(value)
}
