package joinquant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestLoginStoresCookieAndVerifiesSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user/login/doLoginByText", func(response http.ResponseWriter, request *http.Request) {
		if _, exists := request.URL.Query()["ajax"]; exists {
			t.Errorf("password login must mirror the web form without ajax query")
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("username") != "user" || request.Form.Get("pwd") != "secret" {
			t.Errorf("unexpected login form")
		}
		http.SetCookie(response, &http.Cookie{Name: "PHPSESSID", Value: "session-value", Path: "/"})
		fmt.Fprint(response, `{"data":{},"status":"0","code":"00000","msg":""}`)
	})
	mux.HandleFunc("/user/index/isLogin", func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie("PHPSESSID")
		if err != nil || cookie.Value != "session-value" {
			t.Errorf("session cookie missing")
		}
		fmt.Fprint(response, `{"data":{"isLogin":1,"userId":"u1","userName":"tester","alias":"T","vipType":"0","level":1},"status":"0","code":"00000","msg":""}`)
	})

	client := newTestClient(t, mux)
	session, err := client.Login(context.Background(), "user", "secret")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if !session.LoggedIn || session.UserID != "u1" {
		t.Fatalf("session = %#v", session)
	}
	if len(client.Cookies()) != 1 {
		t.Fatalf("cookies = %d, want 1", len(client.Cookies()))
	}
}

func TestRedirectToLoginReturnsSessionExpired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/algorithm/live/stat", func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "/user/login/index", http.StatusFound)
	})
	mux.HandleFunc("/user/login/index", func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, "login")
	})
	client := newTestClient(t, mux)
	_, err := client.Stats(context.Background(), "run-id")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Stats() error = %v, want ErrSessionExpired", err)
	}
}

func TestNumericAPICodeIsPreserved(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, `{"data":{},"status":"2","code":403,"msg":"forbidden"}`)
	}))
	_, err := client.Stats(context.Background(), "run-id")
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "403" {
		t.Fatalf("Stats() error = %#v", err)
	}
}
