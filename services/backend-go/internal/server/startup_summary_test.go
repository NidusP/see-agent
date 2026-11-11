package server

import (
	"strings"
	"testing"

	"see-agent/services/backend-go/internal/repository"
)

func TestBuildStartupSummaryReportsActiveConfigAndStripsSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://db-user:db-secret@127.0.0.1:15432/see_agent?sslmode=disable&token=hidden")
	t.Setenv("REDIS_URL", "redis://redis-user:redis-secret@127.0.0.1:16379/4?token=hidden")
	t.Setenv("AUTH_SESSION_STORE", "redis")
	t.Setenv("AI_RUNTIME_BASE_URL", "http://runtime-user:runtime-secret@127.0.0.1:8000/?token=hidden")
	t.Setenv("OBJECT_STORE_ENDPOINT", "http://minio-user:minio-secret@127.0.0.1:19000?token=hidden")
	t.Setenv("OBJECT_STORE_BUCKET", "knowledge-docs")
	t.Setenv("K3_DELIVERY_ENABLED", "true")
	t.Setenv("LOCAL_LLM_DEFAULT_MODEL_ID", "qwen3:14b")

	output := strings.Join(buildStartupSummary(repository.NewMemoryStore()), "\n")

	for _, expected := range []string{
		"repository=postgres endpoint=127.0.0.1:15432 database=see_agent status=initialized",
		"ingestion_queue=redis endpoint=127.0.0.1:16379 database=4 status=initialized",
		"session_store=redis endpoint=127.0.0.1:16379 database=4 status=initialized",
		"ai_runtime=configured endpoint=http://127.0.0.1:8000",
		"object_store=s3_compatible endpoint=http://127.0.0.1:19000 bucket=knowledge-docs",
		"k3_delivery enabled=true",
		"active_agent_config version=cfg_001 model=qwen3:14b prompt=prompt_v7",
		"active_retrieval_config version=rcfg_001 strategy=native_dense_v1",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("startup summary missing %q:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{"db-secret", "redis-secret", "runtime-secret", "minio-secret", "hidden", "sslmode"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("startup summary leaked %q:\n%s", forbidden, output)
		}
	}
}

func TestBuildStartupSummaryMakesFallbackModesExplicit(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("AUTH_SESSION_STORE", "")
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	t.Setenv("OBJECT_STORE_ENDPOINT", "")
	t.Setenv("OBJECT_STORE_BUCKET", "")
	t.Setenv("K3_DELIVERY_ENABLED", "false")

	output := strings.Join(buildStartupSummary(repository.NewMemoryStore()), "\n")

	for _, expected := range []string{
		"repository=memory status=ephemeral",
		"ingestion_queue=local status=ephemeral",
		"session_store=memory status=ephemeral",
		"ai_runtime=disabled endpoint=none",
		"object_store=local status=ephemeral",
		"k3_delivery enabled=false",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("startup summary missing %q:\n%s", expected, output)
		}
	}
}
