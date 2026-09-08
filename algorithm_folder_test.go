package joinquant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestCreateAlgorithmDirectlyInFolder(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		referer, _ := url.Parse(r.Referer())
		if r.Method != "POST" || r.URL.Path != "/algorithm/index/new" || r.URL.Query().Get("fId") != "folder-12" || r.PostForm.Get("fId") != "folder-12" || referer.Query().Get("fId") != "folder-12" {
			t.Fatal("creation did not target the folder")
		}
		w.Header().Set("Location", "/algorithm/index/edit?algorithmId=created")
		w.WriteHeader(302)
	}))
	id, err := c.CreateAlgorithmInFolder(context.Background(), "folder-12")
	if err != nil || id != "created" || calls != 1 {
		t.Fatalf("%s %v calls=%d", id, err, calls)
	}
	for _, id := range []string{"", "0", " "} {
		if _, err := c.CreateAlgorithmInFolder(context.Background(), id); err == nil {
			t.Fatal("accepted root fallback")
		}
	}
	if calls != 1 {
		t.Fatal("invalid folder contacted server")
	}
}

func TestCreateFolderEncodesNameAndDoesNotReplay(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"code":"00000","status":0,"data":"123"}`, "123"},
		{`{"code":"00000","status":0,"data":123}`, "123"},
		{`{"code":"00000","status":0,"data":null}`, ""},
		{`{"code":"00000","status":0,"data":""}`, ""},
		{`invalid response`, ""},
	} {
		t.Run(tc.body, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				r.ParseForm()
				if r.Method != "POST" || r.URL.Path != "/algorithm/index/AddFile" || r.URL.Query().Get("pId") != "0" || r.PostForm.Get("name") != "JoinQuant 回测台" || r.URL.Query().Get("ajax") != "1" {
					t.Fatal("invalid folder create request")
				}
				fmt.Fprint(w, tc.body)
			}))
			id, err := c.CreateAlgorithmFolder(context.Background(), "JoinQuant 回测台", "")
			if id != tc.want || calls != 1 || (tc.want != "" && err != nil) || (tc.want == "" && !errors.Is(err, ErrSubmissionUnknown)) {
				t.Fatalf("id=%q err=%v calls=%d", id, err, calls)
			}
		})
	}
}

func TestMoveAlgorithmsUsesOperationIDsAndDoesNotFollowRedirect(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		calls := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != "POST" || r.URL.Path != "/algorithm/index/AlgorithmToFile" || r.URL.Query().Get("ids") != "operation-a,operation-b" || r.URL.Query().Get("fId") != "folder-12" {
				t.Fatal("invalid move request")
			}
			if redirect {
				w.Header().Set("Location", "/algorithm/index/AlgorithmToFile")
				w.WriteHeader(307)
				return
			}
			fmt.Fprint(w, `{"code":"00000","status":0,"data":null}`)
		}))
		err := c.MoveAlgorithms(context.Background(), []string{"operation-a", "operation-b"}, "folder-12")
		if calls != 1 || (!redirect && err != nil) || (redirect && !errors.Is(err, ErrSubmissionUnknown)) {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
		if c.MoveAlgorithms(context.Background(), []string{"a,b"}, "folder-12") == nil || c.MoveAlgorithms(context.Background(), []string{"a"}, "0") == nil || calls != 1 {
			t.Fatal("invalid move reached upstream")
		}
	}
}
