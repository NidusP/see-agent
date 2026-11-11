package service

import (
	"errors"
	"testing"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

type configWriteTrackingStore struct {
	*repository.MemoryStore
	activeConfig domain.AgentConfig
	createCalls  int
}

func (s *configWriteTrackingStore) ActiveConfig() domain.AgentConfig {
	return s.activeConfig
}

func (s *configWriteTrackingStore) CreateConfig(modelID, promptVersion, responseStyle, answerPolicyVersion string, temperature, topP float64, maxOutputTokens int) domain.AgentConfig {
	s.createCalls++
	return s.MemoryStore.CreateConfig(modelID, promptVersion, responseStyle, answerPolicyVersion, temperature, topP, maxOutputTokens)
}

func newConfigWriteTrackingStore() *configWriteTrackingStore {
	store := repository.NewMemoryStore()
	return &configWriteTrackingStore{
		MemoryStore:  store,
		activeConfig: store.ActiveConfig(),
	}
}

func TestUpdateConfigRejectsInvalidVersionPairsBeforeStoreWrite(t *testing.T) {
	store := newConfigWriteTrackingStore()
	svc := New(store, nil, "", false)
	defer svc.Close()

	for _, testCase := range []struct {
		name   string
		prompt string
		policy string
	}{
		{name: "v8 blank", prompt: "prompt_v8"},
		{name: "v9 blank", prompt: "prompt_v9"},
		{name: "v10 blank", prompt: "prompt_v10"},
		{name: "v11 blank", prompt: "prompt_v11"},
		{name: "v12 blank", prompt: "prompt_v12"},
		{name: "v8 with v2", prompt: "prompt_v8", policy: domain.AnswerPolicyV2},
		{name: "v8 with v3", prompt: "prompt_v8", policy: domain.AnswerPolicyV3},
		{name: "v9 with v1", prompt: "prompt_v9", policy: domain.AnswerPolicyV1},
		{name: "v9 with v3", prompt: "prompt_v9", policy: domain.AnswerPolicyV3},
		{name: "v10 with v1", prompt: "prompt_v10", policy: domain.AnswerPolicyV1},
		{name: "v10 with v2", prompt: "prompt_v10", policy: domain.AnswerPolicyV2},
		{name: "legacy with v1", prompt: "prompt_v7", policy: domain.AnswerPolicyV1},
		{name: "legacy with v2", prompt: "prompt_v7", policy: domain.AnswerPolicyV2},
		{name: "legacy with v3", prompt: "prompt_v7", policy: domain.AnswerPolicyV3},
		{name: "unknown prompt", prompt: "prompt_unknown"},
		{name: "unknown policy", prompt: "prompt_v10", policy: "answer_policy_v5"},
		{name: "v11 with v5", prompt: "prompt_v11", policy: domain.AnswerPolicyV5},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			beforeConfigs, beforeWrites := len(store.ListConfigs()), store.createCalls
			config, err := svc.UpdateConfig("candidate-model", testCase.prompt, "concise", testCase.policy, .2, 1, 1024)
			if !errors.Is(err, ErrAgentConfigInvalid) {
				t.Fatalf("UpdateConfig error = %v, want ErrAgentConfigInvalid", err)
			}
			if config != (domain.AgentConfig{}) {
				t.Fatalf("invalid config = %#v, want zero value", config)
			}
			if after := len(store.ListConfigs()); after != beforeConfigs {
				t.Fatalf("invalid pair wrote a config: before=%d after=%d", beforeConfigs, after)
			}
			if store.createCalls != beforeWrites {
				t.Fatalf("invalid pair called CreateConfig: before=%d after=%d", beforeWrites, store.createCalls)
			}
			if got := store.ActiveConfig(); got.ConfigVersion != store.activeConfig.ConfigVersion {
				t.Fatalf("invalid pair changed active pointer: got=%#v want=%#v", got, store.activeConfig)
			}
		})
	}
}

func TestUpdateConfigAcceptsAllRegisteredVersionPairsWithoutChangingActivePointer(t *testing.T) {
	store := newConfigWriteTrackingStore()
	svc := New(store, nil, "", false)
	defer svc.Close()

	cases := []struct {
		prompt string
		policy string
	}{
		{prompt: "prompt_v1"},
		{prompt: "prompt_v2"},
		{prompt: "prompt_v3"},
		{prompt: "prompt_v4"},
		{prompt: "prompt_v5"},
		{prompt: "prompt_v6"},
		{prompt: "prompt_v7"},
		{prompt: "prompt_v8", policy: domain.AnswerPolicyV1},
		{prompt: "prompt_v9", policy: domain.AnswerPolicyV2},
		{prompt: "prompt_v10", policy: domain.AnswerPolicyV3},
		{prompt: "prompt_v11", policy: domain.AnswerPolicyV4},
		{prompt: "prompt_v12", policy: domain.AnswerPolicyV5},
	}
	for _, testCase := range cases {
		config, err := svc.UpdateConfig("candidate-model", testCase.prompt, "concise", testCase.policy, .2, 1, 1024)
		if err != nil {
			t.Fatalf("UpdateConfig(%q, %q): %v", testCase.prompt, testCase.policy, err)
		}
		wantPolicy := testCase.policy
		if wantPolicy == "" {
			wantPolicy = domain.LegacyAnswerPolicyVersion
		}
		if config.SystemPromptVersion != testCase.prompt || config.AnswerPolicyVersion != wantPolicy {
			t.Fatalf("created config = %#v", config)
		}
	}
	if store.createCalls != len(cases) {
		t.Fatalf("CreateConfig calls = %d, want %d", store.createCalls, len(cases))
	}
	if got := store.ActiveConfig(); got.ConfigVersion != store.activeConfig.ConfigVersion {
		t.Fatalf("candidate creation changed active pointer: got=%#v want=%#v", got, store.activeConfig)
	}
}
