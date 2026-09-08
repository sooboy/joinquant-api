package joinquant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memorySessionStore struct {
	mu    sync.Mutex
	state map[string]SessionState
	saves int
}

func (s *memorySessionStore) Load(_ context.Context, key string) (*SessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.state[key]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return &state, nil
}

func (s *memorySessionStore) Save(_ context.Context, key string, state SessionState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = make(map[string]SessionState)
	}
	s.state[key] = state
	s.saves++
	return nil
}

func (s *memorySessionStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, key)
	return nil
}

func TestAuthenticateRestoresValidSessionWithoutLogin(t *testing.T) {
	store := &memorySessionStore{state: map[string]SessionState{"user": {
		Version: 1,
		Cookies: []SessionCookie{{Name: "PHPSESSID", Value: "cached", Path: "/", Expires: time.Now().Add(time.Hour)}},
	}}}
	var logins atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/user/index/isLogin", func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("PHPSESSID")
		if cookie == nil || cookie.Value != "cached" {
			fmt.Fprint(w, `{"data":{"isLogin":0},"status":"0","code":"00000"}`)
			return
		}
		fmt.Fprint(w, `{"data":{"isLogin":1,"userId":"u"},"status":"0","code":"00000"}`)
	})
	mux.HandleFunc("/user/login/doLoginByText", func(w http.ResponseWriter, r *http.Request) { logins.Add(1) })
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := New(WithBaseURL(server.URL), WithSessionStore("user", store), WithCredentialProvider(
		CredentialProviderFunc(func(context.Context) (string, string, error) { return "user", "pass", nil }),
	))
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Authenticate(context.Background())
	if err != nil || !info.LoggedIn {
		t.Fatalf("Authenticate() = %#v, %v", info, err)
	}
	if logins.Load() != 0 {
		t.Fatalf("login calls = %d", logins.Load())
	}
}

func TestConcurrentExpiredRequestsReloginOnce(t *testing.T) {
	store := &memorySessionStore{}
	var logins atomic.Int64
	var active atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/user/login/doLoginByText", func(w http.ResponseWriter, r *http.Request) {
		logins.Add(1)
		active.Store(true)
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "new", Path: "/", Expires: time.Now().Add(time.Hour), HttpOnly: true})
		fmt.Fprint(w, `{"data":{},"status":"0","code":"00000"}`)
	})
	mux.HandleFunc("/user/index/isLogin", func(w http.ResponseWriter, r *http.Request) {
		loggedIn := 0
		if active.Load() {
			loggedIn = 1
		}
		fmt.Fprintf(w, `{"data":{"isLogin":%d},"status":"0","code":"00000"}`, loggedIn)
	})
	mux.HandleFunc("/algorithm/live/stat", func(w http.ResponseWriter, r *http.Request) {
		if !active.Load() {
			fmt.Fprint(w, `{"data":{},"status":"0","code":"401"}`)
			return
		}
		fmt.Fprint(w, `{"data":{"data":{}},"status":"0","code":"00000"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := New(WithBaseURL(server.URL), WithSessionStore("user", store), WithCredentialProvider(
		CredentialProviderFunc(func(context.Context) (string, string, error) { return "user", "pass", nil }),
	))
	if err != nil {
		t.Fatal(err)
	}

	const count = 20
	start := make(chan struct{})
	errs := make(chan error, count)
	for range count {
		go func() {
			<-start
			_, err := client.Stats(context.Background(), "run")
			errs <- err
		}()
	}
	close(start)
	for range count {
		if err := <-errs; err != nil {
			t.Errorf("Stats() error = %v", err)
		}
	}
	if logins.Load() != 1 {
		t.Fatalf("login calls = %d, want 1", logins.Load())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state := store.state["user"]
	if len(state.Cookies) != 1 || !state.Cookies[0].HTTPOnly || state.Cookies[0].Path != "/" {
		t.Fatalf("persisted cookies = %#v", state.Cookies)
	}
}

func TestAuthenticateWithoutCredentials(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"isLogin":0},"status":"0","code":"00000"}`)
	}))
	_, err := client.Authenticate(context.Background())
	if !errors.Is(err, ErrCredentialsUnavailable) {
		t.Fatalf("error = %v", err)
	}
}
