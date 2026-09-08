package joinquant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// CredentialProvider supplies credentials only when JoinQuant requires a new
// login. Implementations should use an operating-system secret store.
type CredentialProvider interface {
	Credentials(ctx context.Context) (username, password string, err error)
}

// CredentialProviderFunc adapts a function to CredentialProvider.
type CredentialProviderFunc func(context.Context) (string, string, error)

func (f CredentialProviderFunc) Credentials(ctx context.Context) (string, string, error) {
	return f(ctx)
}

// SessionStore persists the opaque JoinQuant browser session for one account.
type SessionStore interface {
	Load(ctx context.Context, accountKey string) (*SessionState, error)
	Save(ctx context.Context, accountKey string, state SessionState) error
	Delete(ctx context.Context, accountKey string) error
}

type SessionCookie struct {
	Name     string        `json:"name"`
	Value    string        `json:"value"`
	Domain   string        `json:"domain"`
	HostOnly bool          `json:"hostOnly,omitempty"`
	Path     string        `json:"path"`
	Expires  time.Time     `json:"expires,omitempty"`
	MaxAge   int           `json:"maxAge,omitempty"`
	Secure   bool          `json:"secure,omitempty"`
	HTTPOnly bool          `json:"httpOnly,omitempty"`
	SameSite http.SameSite `json:"sameSite,omitempty"`
}

type SessionState struct {
	Version int             `json:"version"`
	Cookies []SessionCookie `json:"cookies"`
	SavedAt time.Time       `json:"savedAt"`
}

func (s SessionState) httpCookies() []*http.Cookie {
	result := make([]*http.Cookie, 0, len(s.Cookies))
	for _, item := range s.Cookies {
		domain := item.Domain
		if item.HostOnly {
			domain = ""
		}
		result = append(result, &http.Cookie{
			Name: item.Name, Value: item.Value, Domain: domain, Path: item.Path,
			Expires: item.Expires, MaxAge: item.MaxAge, Secure: item.Secure,
			HttpOnly: item.HTTPOnly, SameSite: item.SameSite,
		})
	}
	return result
}

// Authenticate restores and verifies a persisted session. It logs in only if
// the restored session is missing or expired.
func (c *Client) Authenticate(ctx context.Context) (*SessionInfo, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()

	if !c.sessionLoaded {
		c.sessionLoaded = true
		if c.sessionStore != nil {
			state, err := c.sessionStore.Load(ctx, c.accountKey)
			switch {
			case err == nil && state != nil:
				c.jar.Replace(c.baseURL, state.httpCookies())
			case err == nil || errors.Is(err, ErrSessionNotFound):
			case err != nil:
				return nil, fmt.Errorf("%w: load: %v", ErrSessionPersistence, err)
			}
		}
	}

	if info, err := c.sessionOnce(ctx); err == nil {
		return info, nil
	} else if !errors.Is(err, ErrSessionExpired) {
		return nil, err
	}
	return c.loginFromProviderLocked(ctx)
}

func (c *Client) currentAuthGeneration() uint64 {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.authGeneration
}

func (c *Client) reauthenticate(ctx context.Context, observedGeneration uint64) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.authGeneration != observedGeneration {
		return nil
	}
	_, err := c.loginFromProviderLocked(ctx)
	return err
}

func (c *Client) loginFromProviderLocked(ctx context.Context) (*SessionInfo, error) {
	if c.credentialProvider == nil {
		return nil, ErrCredentialsUnavailable
	}
	username, password, err := c.credentialProvider.Credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCredentialsUnavailable, err)
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return nil, ErrCredentialsUnavailable
	}
	return c.loginLocked(ctx, username, password)
}

func (c *Client) loginLocked(ctx context.Context, username, password string) (*SessionInfo, error) {
	info, err := c.loginOnce(ctx, username, password)
	if err != nil {
		return nil, err
	}
	c.authGeneration++
	if err := c.saveSession(ctx); err != nil {
		return nil, err
	}
	return info, nil
}

func (c *Client) saveSession(ctx context.Context) error {
	if c.sessionStore == nil {
		return nil
	}
	state := SessionState{Version: 1, Cookies: c.jar.Snapshot(), SavedAt: time.Now().UTC()}
	if err := c.sessionStore.Save(ctx, c.accountKey, state); err != nil {
		return fmt.Errorf("%w: save: %v", ErrSessionPersistence, err)
	}
	return nil
}

// ClearSession removes both in-memory and persisted session cookies.
func (c *Client) ClearSession(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	c.jar.Clear()
	c.sessionLoaded = true
	c.authGeneration++
	if c.sessionStore != nil {
		if err := c.sessionStore.Delete(ctx, c.accountKey); err != nil && !errors.Is(err, ErrSessionNotFound) {
			return fmt.Errorf("%w: delete: %v", ErrSessionPersistence, err)
		}
	}
	return nil
}

type statefulJar struct {
	mu      sync.Mutex
	jar     http.CookieJar
	entries map[string]SessionCookie
}

func newStatefulJar(jar http.CookieJar) *statefulJar {
	return &statefulJar{jar: jar, entries: make(map[string]SessionCookie)}
}

func (j *statefulJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.jar.Cookies(u)
}

func (j *statefulJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.setCookiesLocked(u, cookies)
}

func (j *statefulJar) setCookiesLocked(u *url.URL, cookies []*http.Cookie) {
	j.jar.SetCookies(u, cookies)
	now := time.Now()
	for _, cookie := range cookies {
		hostOnly := cookie.Domain == ""
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if domain == "" {
			domain = strings.ToLower(u.Hostname())
		}
		path := cookie.Path
		if path == "" {
			path = defaultCookiePath(u.Path)
		}
		key := domain + "\x00" + path + "\x00" + cookie.Name
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && !cookie.Expires.After(now)) {
			delete(j.entries, key)
			continue
		}
		expires := cookie.Expires
		if expires.IsZero() && cookie.MaxAge > 0 {
			expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		j.entries[key] = SessionCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
			HostOnly: hostOnly, Expires: expires, MaxAge: 0, Secure: cookie.Secure,
			HTTPOnly: cookie.HttpOnly, SameSite: cookie.SameSite,
		}
	}
}

func (j *statefulJar) Replace(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	jar, _ := cookiejar.New(nil)
	j.jar = jar
	j.entries = make(map[string]SessionCookie)
	j.setCookiesLocked(u, cookies)
}

func (j *statefulJar) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	jar, _ := cookiejar.New(nil)
	j.jar = jar
	j.entries = make(map[string]SessionCookie)
}

func (j *statefulJar) Snapshot() []SessionCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	result := make([]SessionCookie, 0, len(j.entries))
	for key, cookie := range j.entries {
		if !cookie.Expires.IsZero() && !cookie.Expires.After(now) {
			delete(j.entries, key)
			continue
		}
		result = append(result, cookie)
	}
	sort.Slice(result, func(i, k int) bool {
		if result[i].Domain != result[k].Domain {
			return result[i].Domain < result[k].Domain
		}
		if result[i].Path != result[k].Path {
			return result[i].Path < result[k].Path
		}
		return result[i].Name < result[k].Name
	})
	return result
}

func defaultCookiePath(requestPath string) string {
	if requestPath == "" || requestPath[0] != '/' || requestPath == "/" {
		return "/"
	}
	index := strings.LastIndex(requestPath, "/")
	if index <= 0 {
		return "/"
	}
	return requestPath[:index]
}
