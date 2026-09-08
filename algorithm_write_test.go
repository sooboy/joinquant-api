package joinquant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestCreateAlgorithmRedirectAndNoReplay(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/algorithm/index/new" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("ajax") != "" || r.Header.Get("Referer") == "" {
			t.Error("unexpected headers/query")
		}
		w.Header().Set("Location", "/algorithm/index/edit?algorithmId=created&type=empty")
		w.WriteHeader(302)
	}))
	id, e := c.CreateAlgorithm(context.Background())
	if e != nil || id != "created" || calls != 1 {
		t.Fatalf("%q %v %d", id, e, calls)
	}
}
func TestCreateAlgorithmUnknownResponses(t *testing.T) {
	for _, tc := range []struct {
		name, location, body string
		status               int
		session              bool
	}{{"external", "https://external.example/algorithm/index/edit?algorithmId=id", "", 302, false}, {"server", "", `{"code":"50000"}`, 503, false}, {"bad body", "", "bad", 200, false}, {"login", "/user/login", "", 302, true}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			_, e := c.CreateAlgorithm(context.Background())
			target := ErrSubmissionUnknown
			if tc.session {
				target = ErrSessionExpired
			}
			if !errors.Is(e, target) || calls != 1 {
				t.Fatalf("%v calls=%d", e, calls)
			}
		})
	}
}
