package joinquant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var ErrLoginBlocked = errors.New("joinquant login requires user intervention")
var ErrLoginCooldown = errors.New("joinquant login is cooling down")

// LoginState contains no credentials or upstream message text. Pending is saved
// before sending a password, so a crash cannot immediately replay the login.
type LoginState struct {
	Pending    bool      `json:"pending,omitempty"`
	Blocked    bool      `json:"blocked,omitempty"`
	RetryAfter time.Time `json:"retry_after,omitempty"`
	Code       string    `json:"code,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
}

// LoginGuard coordinates clients/processes using the same account key. Load and
// Save are called while holding Lock. Implementations must persist atomically.
type LoginGuard interface {
	Lock(context.Context, string) (func(), error)
	Load(context.Context, string) (LoginState, error)
	Save(context.Context, string, LoginState) error
}

func WithLoginGuard(guard LoginGuard) Option {
	return func(cfg *clientConfig) error {
		if guard == nil {
			return fmt.Errorf("login guard is nil")
		}
		cfg.loginGuard = guard
		return nil
	}
}

// ResetLoginProtection is for an explicit user action after correcting
// credentials or completing verification, never a retry-loop operation.
func (c *Client) ResetLoginProtection(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	unlock, err := c.lockLogin(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return c.storeLoginState(ctx, LoginState{})
}

func (c *Client) lockLogin(ctx context.Context) (func(), error) {
	if c.loginGuard != nil {
		return c.loginGuard.Lock(ctx, c.accountKey)
	}
	return func() {}, ctx.Err()
}

func (c *Client) storeLoginState(ctx context.Context, state LoginState) error {
	c.loginState = state
	if c.loginGuard != nil {
		if err := c.loginGuard.Save(ctx, c.accountKey, state); err != nil {
			return fmt.Errorf("%w: save login protection: %v", ErrSessionPersistence, err)
		}
	}
	return nil
}

func (c *Client) protectedLoginLocked(ctx context.Context, provider CredentialProvider, verify bool) (*SessionInfo, error) {
	unlock, err := c.lockLogin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if c.loginGuard != nil {
		state, err := c.loginGuard.Load(ctx, c.accountKey)
		if err != nil {
			return nil, fmt.Errorf("%w: load login protection: %v", ErrSessionPersistence, err)
		}
		c.loginState = state
	}
	// Always reload after taking the shared lock: another process may have
	// renewed the session while this client was waiting.
	if c.sessionStore != nil && (!c.sessionLoaded || c.loginGuard != nil) {
		state, err := c.sessionStore.Load(ctx, c.accountKey)
		if err != nil && !errors.Is(err, ErrSessionNotFound) {
			return nil, fmt.Errorf("%w: load: %v", ErrSessionPersistence, err)
		}
		if err == nil && state != nil {
			c.jar.Replace(c.baseURL, state.httpCookies())
		}
		c.sessionLoaded = true
	}
	// Explicit Login also reuses an existing session. For a new empty client,
	// only Authenticate needs the preliminary session probe.
	if verify || len(c.jar.Cookies(c.baseURL)) > 0 {
		info, err := c.sessionOnce(ctx)
		if err == nil {
			// A confirmed session resolves an earlier ambiguous login response.
			if c.loginState != (LoginState{}) {
				if err := c.storeLoginState(ctx, LoginState{}); err != nil {
					return nil, err
				}
			}
			return info, nil
		}
		if !errors.Is(err, ErrSessionExpired) {
			return nil, err
		}
	}
	if c.loginState.Blocked || c.loginState.Attempts >= 3 {
		return nil, ErrLoginBlocked
	}
	if time.Now().Before(c.loginState.RetryAfter) {
		return nil, fmt.Errorf("%w until %s", ErrLoginCooldown, c.loginState.RetryAfter.Format(time.RFC3339))
	}
	if provider == nil {
		return nil, ErrCredentialsUnavailable
	}
	username, password, err := provider.Credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCredentialsUnavailable, err)
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return nil, ErrCredentialsUnavailable
	}
	state := LoginState{Pending: true, Attempts: c.loginState.Attempts + 1, RetryAfter: time.Now().Add(5 * time.Minute)}
	if err := c.storeLoginState(ctx, state); err != nil {
		return nil, err
	}
	info, err := c.loginOnce(ctx, username, password)
	if err != nil {
		state.Pending = false
		var api *APIError
		if errors.As(err, &api) {
			state.Code = api.Code
			state.Blocked = api.HTTPStatus != http.StatusTooManyRequests && api.HTTPStatus < 500
		}
		if errors.Is(err, ErrSessionExpired) {
			state.Blocked = true
		}
		state.Blocked = state.Blocked || state.Attempts >= 3
		// Keep the pre-request cooldown even when the request context expired.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return nil, errors.Join(err, c.storeLoginState(saveCtx, state))
	}
	c.authGeneration++
	if err := c.saveSession(ctx); err != nil {
		return nil, err
	}
	if err := c.storeLoginState(ctx, LoginState{}); err != nil {
		return nil, err
	}
	return info, nil
}
