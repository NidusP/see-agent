package server

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRuntimeProfileRejectsImplicitFullstackDefaults(t *testing.T) {
	t.Setenv("SEE_AGENT_PROFILE", "fullstack_dev")
	t.Setenv("DATABASE_URL", "")

	if err := validateRuntimeProfile(); err == nil {
		t.Fatal("fullstack profile accepted a missing DATABASE_URL")
	}
}

func TestValidateRuntimeProfileAcceptsExplicitFullstackDependencies(t *testing.T) {
	setValidFullstackEnvironment(t, "fullstack_dev")

	if err := validateRuntimeProfile(); err != nil {
		t.Fatalf("fullstack profile rejected explicit dependencies: %v", err)
	}
}

func TestConfiguredRuntimeChatTimeout(t *testing.T) {
	t.Setenv("BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS", "60")
	timeout, err := configuredRuntimeChatTimeout()
	if err != nil || timeout != 60*time.Second {
		t.Fatalf("timeout=%s err=%v", timeout, err)
	}
	for _, value := range []string{"", "4", "601", "invalid"} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS", value)
			_, err := configuredRuntimeChatTimeout()
			if value == "" {
				if err != nil {
					t.Fatalf("default timeout error=%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("value %q unexpectedly accepted", value)
			}
		})
	}
}

func TestValidateRuntimeProfileEnforcesRuntimeServiceTokenPolicy(t *testing.T) {
	validCurrent := strings.Repeat("c", 32)
	validPrevious := strings.Repeat("p", 32)
	tests := []struct {
		name     string
		profile  string
		current  string
		previous string
		wantErr  string
	}{
		{name: "missing current", profile: "fullstack_dev", current: "", wantErr: "SEE_AGENT_RUNTIME_SERVICE_TOKEN"},
		{name: "placeholder current", profile: "fullstack_dev", current: runtimeServiceTokenPlaceholder, wantErr: "placeholder"},
		{name: "short current", profile: "production", current: "short-secret", wantErr: "at least 32"},
		{name: "valid current", profile: "fullstack_dev", current: validCurrent},
		{name: "valid previous", profile: "production", current: validCurrent, previous: validPrevious},
		{name: "short previous", profile: "fullstack_dev", current: validCurrent, previous: "short-secret", wantErr: "PREVIOUS must be at least 32"},
		{name: "previous equals current", profile: "production", current: validCurrent, previous: validCurrent, wantErr: "PREVIOUS must differ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidFullstackEnvironment(t, tt.profile)
			t.Setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", tt.current)
			t.Setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", tt.previous)

			err := validateRuntimeProfile()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("profile rejected valid token policy: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRuntimeProfile() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func setValidFullstackEnvironment(t *testing.T, profile string) {
	t.Helper()
	t.Setenv("SEE_AGENT_PROFILE", profile)
	for key, value := range map[string]string{
		"DATABASE_URL":                       "postgres://localhost/see_agent",
		"REDIS_URL":                          "redis://localhost:6379/0",
		"AI_RUNTIME_BASE_URL":                "http://localhost:8000",
		"SEE_AGENT_RUNTIME_SERVICE_TOKEN":    strings.Repeat("c", 32),
		"OBJECT_STORE_ENDPOINT":              "http://localhost:9000",
		"OBJECT_STORE_BUCKET":                "knowledge-docs",
		"OBJECT_STORE_ACCESS_KEY":            "test-access",
		"OBJECT_STORE_SECRET_KEY":            "test-secret",
		"SEE_AGENT_SYSTEM_ADMIN_USERNAME":    "system",
		"SEE_AGENT_SYSTEM_ADMIN_PASSWORD":    "system-password",
		"SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME": "knowledge",
		"SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD": "knowledge-password",
		"SEE_AGENT_USER_USERNAME":            "user",
		"SEE_AGENT_USER_PASSWORD":            "user-password",
		"AUTH_SESSION_STORE":                 "redis",
		"K3_DELIVERY_ENABLED":                "true",
	} {
		t.Setenv(key, value)
	}
	t.Setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", "")
}
