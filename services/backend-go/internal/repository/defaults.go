package repository

import (
	"os"
	"strings"
)

const fallbackLocalLLMModelID = "qwen3:14b"

func defaultAgentModelID() string {
	if modelID := strings.TrimSpace(os.Getenv("LOCAL_LLM_DEFAULT_MODEL_ID")); modelID != "" {
		return modelID
	}
	return fallbackLocalLLMModelID
}
