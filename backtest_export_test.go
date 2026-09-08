package joinquant

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func exportButtons(w io.Writer) {
	fmt.Fprint(w, `<a id="export-transaction-button" type="transaction" backtestid="transactions-only">交易</a><a id="export-position-button" type="position" backtestid="positions-only">持仓</a><a id="export-log-button" type="log" backtestid="logs-only">日志</a>`)
}

func TestExportsUsePurposeSpecificIDsAndNeverCredits(t *testing.T) {
	ids := map[string]string{"transaction": "transactions-only", "position": "positions-only", "log": "logs-only"}
	writes := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/algorithm/backtest/detail":
			w.Header().Set("Content-Type", "text/html")
			if r.URL.Query().Get("backtestId") != "detail" {
				t.Error("wrong detail ID")
			}
			exportButtons(w)
		case "/algorithm/backtest/addExportZip":
			writes++
			q := r.URL.Query()
			if r.Method != "GET" || q.Get("useCredit") != "0" || q.Get("backtestId") != ids[q.Get("type")] || r.Referer() == "" {
				t.Errorf("incorrect export request: %s", r.URL.Path)
			}
			b, e := io.ReadAll(r.Body)
			if e != nil || len(b) != 0 {
				t.Error("expected an empty body")
			}
			fmt.Fprint(w, `{"code":"00000","status":"0","data":"export-task"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	for _, kind := range []BacktestExportKind{ExportTransactions, ExportPositions, ExportLogs} {
		task, e := c.CreateBacktestExport(context.Background(), "detail", kind)
		if e != nil || task != "export-task" {
			t.Fatalf("%s: %s %v", kind, task, e)
		}
	}
	if writes != 3 {
		t.Fatal(writes)
	}
	if _, e := c.CreateBacktestExport(context.Background(), "detail", ExportSummary); e == nil {
		t.Fatal("summary must not consume a ZIP entitlement")
	}
}

func TestExportRejectionsAndUnknownResponsesAreNotReplayed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"credits", `{"code":30000,"status":"0","data":{"ruleKey":"export_log"}}`, 200, ErrExportCreditsRequired},
		{"malformed", `broken`, 200, ErrSubmissionUnknown},
		{"missing task", `{"code":"00000","data":""}`, 200, ErrSubmissionUnknown},
		{"server failure", `{}`, 503, ErrSubmissionUnknown},
		{"redirect", ``, 307, ErrSubmissionUnknown},
		{"session", `{"code":"10001","msg":"login"}`, 200, ErrSessionExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/algorithm/backtest/detail" {
					w.Header().Set("Content-Type", "text/html")
					exportButtons(w)
					return
				}
				writes++
				w.Header().Set("Location", "/algorithm/backtest/addExportZip")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			_, e := c.CreateBacktestExport(context.Background(), "detail", ExportLogs)
			if !errors.Is(e, tc.want) || writes != 1 {
				t.Fatalf("writes=%d error=%v", writes, e)
			}
		})
	}
}

func TestExportGETTransportDoesNotReplayAfterConnectionLoss(t *testing.T) {
	var writes atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/algorithm/backtest/detail" {
			w.Header().Set("Content-Type", "text/html")
			exportButtons(w)
			return
		}
		writes.Add(1)
		io.Copy(io.Discard, r.Body)
		conn, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		conn.Close()
	}))
	_, e := c.CreateBacktestExport(context.Background(), "detail", ExportPositions)
	if !errors.Is(e, ErrSubmissionUnknown) || writes.Load() != 1 {
		t.Fatalf("GET replayed: writes=%d error=%v", writes.Load(), e)
	}
}

func TestExportStatusAndDownloadValidation(t *testing.T) {
	for _, status := range []string{`false`, `0`, `"1"`, `"2"`} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"code":"00000","data":%s}`, status) }))
		if _, e := c.BacktestExportStatus(context.Background(), "task"); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		body, content string
		valid         bool
	}{
		{"PK\x03\x04archive", "application/zip", true},
		{`{"code":"10001","msg":"login"}`, "application/json", false},
		{`<html>login</html>`, "text/html", false},
		{"not a zip", "application/octet-stream", false},
	} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("task") != "task" {
				t.Error("wrong task")
			}
			w.Header().Set("Content-Type", tc.content)
			fmt.Fprint(w, tc.body)
		}))
		var dst bytes.Buffer
		e := c.DownloadBacktestExport(context.Background(), "task", &dst)
		if (e == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.content, e)
		}
		if !tc.valid && dst.Len() != 0 {
			t.Fatal("error page written as export")
		}
	}
}
