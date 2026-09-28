// Package webkey keeps browser-provided DeepSeek credentials in process memory.
// It never persists or returns a credential to a browser.
package webkey

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const cookieName = "cc_agent_key_session"
const sessionLifetime = 12 * time.Hour

type session struct {
	key       string
	expiresAt time.Time
}

// Store isolates a credential by an unguessable, HttpOnly browser cookie.
type Store struct {
	mu       sync.Mutex
	sessions map[string]session
}

func NewStore() *Store {
	return &Store{sessions: make(map[string]session)}
}

func secureCookie(request *http.Request) bool {
	return request.TLS != nil || request.Header.Get("X-Forwarded-Proto") == "https" ||
		strings.HasPrefix(request.Header.Get("Origin"), "https://")
}

func (store *Store) token(request *http.Request) string {
	cookie, err := request.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// Scope returns a stable, credential-isolated runtime scope for this browser.
func (store *Store) Scope(request *http.Request) string {
	token := store.token(request)
	if _, found := store.KeyForToken(token); found {
		return token
	}
	return "server"
}

func (store *Store) Key(request *http.Request) (string, bool) {
	return store.KeyForToken(store.token(request))
}

// KeyForToken is used when a background agent starts another task, so a
// replaced or removed browser credential is not reused by that task.
func (store *Store) KeyForToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	stored, found := store.sessions[token]
	if !found {
		return "", false
	}
	if time.Now().After(stored.expiresAt) {
		delete(store.sessions, token)
		return "", false
	}
	return stored.key, true
}

// Put replaces the credential for this browser, creating a session if needed.
// The key is never included in a response body or a readable cookie.
func (store *Store) Put(writer http.ResponseWriter, request *http.Request, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 4096 {
		return errors.New("DeepSeek API Key 长度无效")
	}
	token := store.token(request)
	if _, found := store.KeyForToken(token); !found {
		randomBytes := make([]byte, 32)
		if _, err := rand.Read(randomBytes); err != nil {
			return errors.New("无法创建浏览器凭据会话")
		}
		token = base64.RawURLEncoding.EncodeToString(randomBytes)
	}
	store.mu.Lock()
	store.sessions[token] = session{key: key, expiresAt: time.Now().Add(sessionLifetime)}
	store.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true,
		Secure:   secureCookie(request),
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds()),
	})
	return nil
}

func (store *Store) Delete(writer http.ResponseWriter, request *http.Request) {
	store.mu.Lock()
	delete(store.sessions, store.token(request))
	store.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name: cookieName, Path: "/", HttpOnly: true,
		Secure:   secureCookie(request),
		SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}
