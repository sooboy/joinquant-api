package joinquant

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestSessionSeparatesRotatingPageIDFromAccountKey(t *testing.T) {
	var requests atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user/index/isLogin" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprintf(w, `{"code":"00000","data":{"isLogin":1,"userId":"opaque-%d","uniqueKey":"stable-account-key","userName":"alice"}}`, requests.Add(1))
	}))
	first, err := c.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.UserID == second.UserID || first.UniqueKey != "stable-account-key" || first.UniqueKey != second.UniqueKey {
		t.Fatal("opaque IDs must not replace stable account identity")
	}
}
