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

type testLoginGuard struct {
	mu    sync.Mutex
	state LoginState
}

func (g *testLoginGuard) Lock(ctx context.Context, _ string) (func(), error) {
	g.mu.Lock()
	return g.mu.Unlock, nil
}
func (g *testLoginGuard) Load(context.Context, string) (LoginState, error) { return g.state, nil }
func (g *testLoginGuard) Save(_ context.Context, _ string, s LoginState) error {
	g.state = s
	return nil
}

func TestLoginGuardSharesSuccessAndFailureAcrossClients(t *testing.T) {
	for _, mode := range []string{"success", "password", "network", "crash"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("/user/index/isLogin", func(w http.ResponseWriter, r *http.Request) {
				cookie, _ := r.Cookie("sid")
				if cookie != nil && cookie.Value == "valid" {
					fmt.Fprint(w, `{"code":"00000","data":{"isLogin":1,"userId":"one"}}`)
					return
				}
				fmt.Fprint(w, `{"code":"00000","data":{"isLogin":0}}`)
			})
			mux.HandleFunc("/user/login/doLoginByText", func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				switch mode {
				case "success":
					http.SetCookie(w, &http.Cookie{Name: "sid", Value: "valid", Path: "/"})
					fmt.Fprint(w, `{"code":"00000","data":{}}`)
				case "password":
					fmt.Fprint(w, `{"code":"20000","msg":"password or verification required"}`)
				default:
					http.Error(w, "unavailable", 503)
				}
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			store := &memorySessionStore{}
			guard := &testLoginGuard{}
			if mode == "crash" {
				guard.state = LoginState{Pending: true, RetryAfter: time.Now().Add(time.Minute)}
			}
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					c, e := New(WithBaseURL(server.URL), WithSessionStore("one", store), WithLoginGuard(guard), WithCredentialProvider(CredentialProviderFunc(func(context.Context) (string, string, error) { return "one", "secret", nil })))
					if e != nil {
						t.Error(e)
						return
					}
					_, e = c.Authenticate(context.Background())
					if (mode == "success") != (e == nil) {
						t.Errorf("authentication error=%v", e)
					}
				}()
			}
			wg.Wait()
			want := int64(1)
			if mode == "crash" {
				want = 0
			}
			if posts.Load() != want {
				t.Fatalf("password posts=%d want %d", posts.Load(), want)
			}
		})
	}
}

func TestLoginFailureCooldownWithoutExternalGuard(t *testing.T) {
	var posts atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			http.Error(w, "unavailable", 503)
			return
		}
		fmt.Fprint(w, `{"code":"00000","data":{"isLogin":0}}`)
	}))
	_, _ = c.Login(context.Background(), "one", "secret")
	_, err := c.Login(context.Background(), "one", "secret")
	if !errors.Is(err, ErrLoginCooldown) || posts.Load() != 1 {
		t.Fatalf("cooldown=%v posts=%d", err, posts.Load())
	}
	if err := c.ResetLoginProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = c.Login(context.Background(), "one", "corrected")
	if posts.Load() != 2 {
		t.Fatal("explicit reset did not allow login")
	}
}

func TestSessionReadFailureDoesNotRequestCredentialsOrLogin(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 503) }))
	defer server.Close()
	c, _ := New(WithBaseURL(server.URL), WithCredentialProvider(CredentialProviderFunc(func(context.Context) (string, string, error) { calls.Add(1); return "one", "secret", nil })))
	_, err := c.Authenticate(context.Background())
	if err == nil || calls.Load() != 0 {
		t.Fatalf("session failure triggered credentials: %v %d", err, calls.Load())
	}
}
