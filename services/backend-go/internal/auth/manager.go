package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	SessionCookieName = "see_agent_session"
	RoleCookieName    = "see_agent_role"

	RoleUser           = "user"
	RoleKnowledgeAdmin = "knowledge_admin"
	RoleSystemAdmin    = "system_admin"
	// RoleAdmin is retained for Go callers that historically referred to the
	// all-powerful administrator. New sessions use RoleSystemAdmin instead.
	RoleAdmin       = RoleSystemAdmin
	RoleLegacyAdmin = "admin"
)

const defaultSessionTTL = 8 * time.Hour

type User struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type Credential struct {
	User     User
	Password string
}

type Manager struct {
	mu           sync.RWMutex
	users        map[string]Credential
	sessionStore SessionStore
	ttl          time.Duration
}

type SessionRecord struct {
	User      User      `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SessionStore interface {
	Save(ctx context.Context, token string, record SessionRecord) error
	Find(ctx context.Context, token string) (SessionRecord, bool, error)
	Delete(ctx context.Context, token string) error
	Close() error
}

type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]SessionRecord
}

func NewDefaultManager() *Manager {
	return NewManager(DefaultCredentialsFromEnv(), defaultSessionTTL)
}

func DefaultSessionTTL() time.Duration {
	return defaultSessionTTL
}

func NewManager(credentials []Credential, ttl time.Duration) *Manager {
	return NewManagerWithSessionStore(credentials, ttl, NewMemorySessionStore())
}

func NewManagerWithSessionStore(credentials []Credential, ttl time.Duration, sessionStore SessionStore) *Manager {
	if sessionStore == nil {
		sessionStore = NewMemorySessionStore()
	}
	manager := &Manager{
		users:        make(map[string]Credential),
		sessionStore: sessionStore,
		ttl:          ttl,
	}
	for _, credential := range credentials {
		username := strings.TrimSpace(credential.User.Username)
		password := credential.Password
		if username == "" || password == "" {
			continue
		}
		credential.User.Username = username
		manager.users[username] = credential
	}
	return manager
}

func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: make(map[string]SessionRecord)}
}

func DefaultCredentialsFromEnv() []Credential {
	systemAdminUsername := envFirst("admin", "SEE_AGENT_SYSTEM_ADMIN_USERNAME", "SEE_AGENT_ADMIN_USERNAME")
	systemAdminPassword := envFirst("admin123", "SEE_AGENT_SYSTEM_ADMIN_PASSWORD", "SEE_AGENT_ADMIN_PASSWORD")
	knowledgeAdminUsername := envOrDefault("SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME", "knowledge_admin")
	knowledgeAdminPassword := envOrDefault("SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD", "knowledge_admin123")
	userUsername := envOrDefault("SEE_AGENT_USER_USERNAME", "user")
	userPassword := envOrDefault("SEE_AGENT_USER_PASSWORD", "user123")

	return []Credential{
		{
			User: User{
				// Retain the established subject so K9-S0 legacy conversation
				// ownership remains readable after the role split.
				UserID:   "user_admin",
				Username: systemAdminUsername,
				Role:     RoleSystemAdmin,
			},
			Password: systemAdminPassword,
		},
		{
			User: User{
				UserID:   "user_knowledge_admin",
				Username: knowledgeAdminUsername,
				Role:     RoleKnowledgeAdmin,
			},
			Password: knowledgeAdminPassword,
		},
		{
			User: User{
				UserID:   "user_member",
				Username: userUsername,
				Role:     RoleUser,
			},
			Password: userPassword,
		},
	}
}

func IsKnowledgeAdminRole(role string) bool {
	return role == RoleKnowledgeAdmin || IsSystemAdminRole(role)
}

func IsSystemAdminRole(role string) bool {
	return role == RoleSystemAdmin || role == RoleLegacyAdmin
}

func IsKnownRole(role string) bool {
	return role == RoleUser || role == RoleKnowledgeAdmin || IsSystemAdminRole(role)
}

func (m *Manager) Authenticate(username, password string) (User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	credential, ok := m.users[strings.TrimSpace(username)]
	if !ok {
		return User{}, false
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(credential.Password)) != 1 {
		return User{}, false
	}
	return credential.User, true
}

func (m *Manager) CreateSession(user User) (string, time.Time, error) {
	token, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().UTC().Add(m.ttl)

	if err := m.sessionStore.Save(context.Background(), token, SessionRecord{User: user, ExpiresAt: expiresAt}); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (m *Manager) UserFromRequest(r *http.Request) (User, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return User{}, false
	}
	return m.userForToken(r.Context(), cookie.Value)
}

func (m *Manager) UserForToken(token string) (User, bool) {
	return m.userForToken(context.Background(), token)
}

func (m *Manager) userForToken(ctx context.Context, token string) (User, bool) {
	record, ok, err := m.sessionStore.Find(ctx, token)
	if err != nil || !ok {
		return User{}, false
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		m.ClearSession(token)
		return User{}, false
	}
	return record.User, true
}

func (m *Manager) ClearSession(token string) {
	if strings.TrimSpace(token) == "" {
		return
	}
	_ = m.sessionStore.Delete(context.Background(), token)
}

func (m *Manager) SessionMaxAgeSeconds() int {
	return int(m.ttl.Seconds())
}

func (s *MemorySessionStore) Save(_ context.Context, token string, record SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = record
	return nil
}

func (s *MemorySessionStore) Find(_ context.Context, token string) (SessionRecord, bool, error) {
	s.mu.RLock()
	record, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok {
		return SessionRecord{}, false, nil
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		_ = s.Delete(context.Background(), token)
		return SessionRecord{}, false, nil
	}
	return record, true, nil
}

func (s *MemorySessionStore) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	return nil
}

func (s *MemorySessionStore) Close() error {
	return nil
}

func SetLoginCookies(w http.ResponseWriter, token string, user User, maxAgeSeconds int) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     RoleCookieName,
		Value:    user.Role,
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearLoginCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     RoleCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}

func newToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envFirst(fallback string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}
