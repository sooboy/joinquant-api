package joinquant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBacktestNumbersPreserveMissingAndUnits(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		want       float64
		valid, bad bool
	}{
		{`0`, 0, true, false}, {`"12.5%"`, .125, true, false},
		{`"-1,234.50"`, -1234.5, true, false}, {`""`, 0, false, false},
		{`null`, 0, false, false}, {`"N/A"`, 0, false, false},
		{`"NaN"`, 0, false, true}, {`"Infinity"`, 0, false, true},
		{`"profit=12"`, 0, false, true}, {`"1,2"`, 0, false, true}, {`true`, 0, false, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			n := OptionalNumber{Value: 9, Valid: true}
			err := json.Unmarshal([]byte(tc.raw), &n)
			if (err != nil) != tc.bad {
				t.Fatalf("error = %v", err)
			}
			if !tc.bad && (n.Valid != tc.valid || n.Value != tc.want) {
				t.Fatalf("number = %+v", n)
			}
		})
	}
	for _, raw := range []string{`1.5`, `"2.3"`, `null`, `"999999999999999999999999"`} {
		var n IntValue
		if json.Unmarshal([]byte(raw), &n) == nil {
			t.Errorf("accepted invalid integer %s", raw)
		}
	}
	var state BacktestState
	if err := json.Unmarshal([]byte(`2`), &state); err != nil || !state.Terminal() {
		t.Fatalf("state = %s, %v", state, err)
	}
	if BacktestState("99").Terminal() {
		t.Fatal("unknown state treated as terminal")
	}
}

func TestBacktestPositionsIncludeCashAndReportEmptyTruncation(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/algorithm/backtest/positionInfo" {
			t.Errorf("unexpected request")
		}
		if r.URL.Query().Get("offset") == "" {
			fmt.Fprint(w, `{"code":"00000","status":0,"data":{"status":2,"position":[
{"date":"2026-01-05","stock":"示例(000001.XSHE)","trueAmount":"100股","value":1000,"gain":0,"totalValue":990},
{"date":"2026-01-05","stock":"<span class=\"label label-cash\">Cash</span>","value":-10,"gain":"","margin":"","dailyGains":"","totalValue":0}],"max":false}}`)
		} else {
			if r.URL.Query().Get("offset") != "2" || r.URL.Query().Get("dateOffset") != "2026-01-05" {
				t.Errorf("wrong cursor: %v", r.URL.Query())
			}
			fmt.Fprint(w, `{"code":"00000","status":"0","data":{"status":"2","position":[],"max":true}}`)
		}
	}))
	p, err := client.BacktestPositionPage(context.Background(), "run", BacktestCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Done || len(p.Positions) != 2 || p.NextCursor.Offset != 2 {
		t.Fatalf("page = %+v", p)
	}
	stock, cash := p.Positions[0], p.Positions[1]
	if stock.IsCash() || stock.Code() != "000001.XSHE" || !stock.Gain.Valid {
		t.Fatalf("stock = %+v", stock)
	}
	if !cash.IsCash() || cash.Code() != "" || cash.Gain.Valid || cash.Value.Value != -10 {
		t.Fatalf("cash = %+v", cash)
	}
	if _, err := cash.Quantity(); err == nil {
		t.Fatal("cash has security quantity")
	}
	p, err = client.BacktestPositionPage(context.Background(), "run", p.NextCursor)
	if !errors.Is(err, ErrTruncated) || p == nil || !p.Max || p.Done {
		t.Fatalf("truncated page = %+v, %v", p, err)
	}
}

func TestBacktestTransactionsShortPageStillAdvancesUsingTradeDate(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") == "" {
			rows := make([]map[string]any, 99)
			for i := range rows {
				rows[i] = map[string]any{"date": "2026-01-01", "tradeDate": "2026-01-05", "trueAmount": "0股", "total": "展示(1,234.5)"}
			}
			json.NewEncoder(w).Encode(map[string]any{"code": "00000", "status": "0", "data": map[string]any{"status": "2", "transaction": rows, "max": false}})
		} else {
			if r.URL.Query().Get("offset") != "99" || r.URL.Query().Get("dateOffset") != "2026-01-05" {
				t.Errorf("wrong cursor: %v", r.URL.Query())
			}
			fmt.Fprint(w, `{"code":"00000","status":"0","data":{"status":"2","transaction":[],"max":false}}`)
		}
	}))
	p, err := client.BacktestTransactionPage(context.Background(), "run", BacktestCursor{})
	if err != nil || p.Done || p.NextCursor.Offset != 99 {
		t.Fatalf("first page = %+v, %v", p, err)
	}
	n, err := p.Transactions[0].TotalAmount()
	if err != nil || !n.Valid || n.Value != 1234.5 {
		t.Fatalf("total = %+v, %v", n, err)
	}
	p, err = client.BacktestTransactionPage(context.Background(), "run", p.NextCursor)
	if err != nil || !p.Done {
		t.Fatalf("last page = %+v, %v", p, err)
	}
}

func TestBacktestResultCompletedTaskStillHasUnreadPages(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("userRecordOffset") != "7" {
			t.Error("custom cursor was overwritten by curve cursor")
		}
		if r.URL.Query().Get("offset") == "0" {
			fmt.Fprint(w, `{"code":"00000","status":"0","data":{"state":"2","result":{"offset":"0","count":"1","benchmark":{"time":[1000],"value":[2]},"overallReturn":{"time":[1000],"value":[12.5]}},"userRecord":null}}`)
		} else {
			fmt.Fprint(w, `{"code":"00000","status":"0","data":{"state":2,"result":{"offset":1,"count":0,"benchmark":{"time":[],"value":[]},"overallReturn":{"time":[],"value":[]}},"userRecord":null}}`)
		}
	}))
	p, err := client.BacktestResultPage(context.Background(), "run", BacktestResultOptions{UserRecordOffset: 7})
	if err != nil || p.Done || p.NextOffset != 1 || p.Result.OverallReturn.Value[0].Value != 12.5 {
		t.Fatalf("page = %+v, %v", p, err)
	}
	p, err = client.BacktestResultPage(context.Background(), "run", BacktestResultOptions{Offset: p.NextOffset, UserRecordOffset: 7})
	if err != nil || !p.Done {
		t.Fatalf("page = %+v, %v", p, err)
	}
}

func TestBacktestResultRejectsBrokenCursorsAndSchemas(t *testing.T) {
	for _, data := range []string{
		`{"state":2,"result":{"offset":0,"count":1,"benchmark":{"time":[],"value":[]},"overallReturn":{"time":[1],"value":[2]}}}`,
		`{"state":2,"result":{"offset":99,"count":0}}`, `{"state":2,"result":null}`, `{}`,
		`{"state":2,"result":{}}`,
	} {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"code":"00000","status":"0","data":%s}`, data)
		}))
		if _, err := client.BacktestResultPage(context.Background(), "run", BacktestResultOptions{}); !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("accepted %s: %v", data, err)
		}
	}
}

func TestBacktestReadAuxiliaryEndpoints(t *testing.T) {
	responses := map[string]string{
		"stats":       `{"algorithm_return":0.125,"avg_excess_return":"0.11%","max_drawdown_period":["2026-01-01","2026-02-01"]}`,
		"statsList":   `[{"backtestId":"list-run","stat":{"algorithm_return":0.125},"usedSec":"1秒"}]`,
		"source":      `{"source":"def initialize(context):\n    pass\n"}`,
		"runTimeInfo": `{"status":2,"cancelTime":"0000-00-00 00:00:00","creditsLog":[]}`,
		"profile":     `{"state":"2","profile":""}`,
		"risk":        `{"risk":{"sharp":[{"date":"2026-01","1month":0,"3month":"N/A"}]}}`,
		"log":         `{"state":2,"offset":"3","logArr":["line 1","line 2"],"max":false}`,
		"error":       `{"state":3,"logArr":[]}`,
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("ajax") != "1" {
			t.Errorf("unexpected request")
		}
		name := strings.TrimPrefix(r.URL.Path, "/algorithm/backtest/")
		if name == "source" && r.URL.Query().Get("backtestId") != "source-id" {
			t.Error("wrong source identifier")
		}
		if name == "statsList" && (r.URL.Query().Get("backtestIds") != "list-run" || r.URL.Query().Get("algorithmId") != "history-id") {
			t.Error("wrong batch identifiers")
		}
		if name == "statsList" && !strings.Contains(r.Referer(), "/algorithm/backtest/list?algorithmId=history-id") {
			t.Error("missing batch referer")
		}
		data, ok := responses[name]
		if !ok {
			t.Errorf("unexpected endpoint %s", name)
		}
		fmt.Fprintf(w, `{"code":"00000","status":0,"data":%s}`, data)
	}))
	ctx := context.Background()
	s, err := client.BacktestStats(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.Number("algorithm_return")
	if err != nil || n.Value != .125 {
		t.Fatalf("metric = %+v %v", n, err)
	}
	n, err = s.Number("avg_excess_return")
	if err != nil || n.Value != .0011 {
		t.Fatalf("percent = %+v %v", n, err)
	}
	if _, err := s.Number("max_drawdown_period"); err == nil {
		t.Fatal("range accepted as scalar")
	}
	if _, err := client.BacktestBatchStats(ctx, "history-id", "list-run"); err != nil {
		t.Fatal(err)
	}
	if source, err := client.BacktestSource(ctx, "source-id"); err != nil || !strings.Contains(source, "\n    pass\n") {
		t.Fatalf("source = %q %v", source, err)
	}
	if _, err := client.BacktestRuntime(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.BacktestProfile(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	risk, err := client.BacktestRisk(ctx, "run")
	if err != nil || risk["sharp"][0].Month3.Valid {
		t.Fatalf("risk = %+v %v", risk, err)
	}
	logs, err := client.BacktestLogs(ctx, "run", 3)
	if err != nil || logs.NextOffset != 5 || logs.Done {
		t.Fatalf("logs = %+v %v", logs, err)
	}
	logs, err = client.BacktestErrors(ctx, "run", 5)
	if err != nil || logs.NextOffset != 5 || !logs.Done {
		t.Fatalf("errors = %+v %v", logs, err)
	}
}

const testEditorHTML = `<html><script>window.tokenData = {name: "token", value: "page-token"}</script>
<form><input id="algorithmId" name="algorithm[algorithmId]" value="fresh-operation">
<input name="algorithm[userId]" value="user-field"><input name="algorithm[accessControl]" value="0">
<input name="algorithm[name]" value="测试策略"><input name="useCredit" value="1">
<textarea id="code" name="algorithm[code]">def initialize(context):
    pass
</textarea></form></html>`

func testBuildOptions() BuildBacktestOptions {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	return BuildBacktestOptions{StartTime: time.Date(2026, 1, 5, 0, 0, 0, 0, zone), EndTime: time.Date(2026, 1, 6, 23, 59, 59, 0, zone), InitialCapital: 100000, Frequency: BacktestDaily}
}

func TestBuildBacktestFreshFormAndUTF8Source(t *testing.T) {
	source := "# 中文 + & = ?\ndef initialize(context):\n    log.info('测试')\n"
	var builds atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/algorithm/index/edit":
			if r.URL.Query().Get("algorithmId") != "edit-id" || r.URL.Query().Get("ajax") != "" || r.Header.Get("X-Requested-With") != "" {
				t.Error("incorrect editor request")
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, testEditorHTML)
		case "/algorithm/index/build":
			builds.Add(1)
			if !strings.HasSuffix(r.Referer(), "/algorithm/index/edit?algorithmId=edit-id") || r.Header.Get("Origin") == "" {
				t.Error("missing browser request context")
			}
			if r.Method != "POST" {
				t.Error("build must be POST")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			for k, want := range map[string]string{"algorithm[algorithmId]": "fresh-operation", "token": "page-token", "encrType": "base64", "backtest[type]": "0", "backtest[pyVersion]": "3", "backtest[startTime]": "2026-01-05 00:00:00", "backtest[endTime]": "2026-01-06 23:59:59", "backtest[baseCapital]": "100000", "backtest[frequency]": "day"} {
				if r.PostForm.Get(k) != want {
					t.Errorf("form field %s mismatch", k)
				}
			}
			if r.PostForm.Get("useCredit") != "" {
				t.Error("page credit preference was inherited")
			}
			decoded, err := base64.StdEncoding.DecodeString(r.PostForm.Get("algorithm[code]"))
			if err != nil || string(decoded) != source {
				t.Error("source encoding corrupted")
			}
			fmt.Fprint(w, `{"code":"00000","status":"0","data":{"algorithmId":"new-operation","backtestId":"created-detail","backtestId_":123,"tradeDays":[1000]}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	o := testBuildOptions()
	o.Source = &source
	r, err := client.BuildBacktest(context.Background(), "edit-id", o)
	if err != nil || r.BacktestID != "created-detail" || builds.Load() != 1 {
		t.Fatalf("build = %+v %v, count %d", r, err, builds.Load())
	}
}

func TestBuildBacktestNeverReplaysRejectedOrUncertainWrites(t *testing.T) {
	for _, mode := range []string{"disconnect", "redirect", "server-error", "invalid-json", "missing-id", "missing-code", "expired", "quota"} {
		t.Run(mode, func(t *testing.T) {
			var writes, credentials atomic.Int32
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/algorithm/index/edit" {
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, testEditorHTML)
					return
				}
				writes.Add(1)
				switch mode {
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
				case "redirect":
					http.Redirect(w, r, "/replay", http.StatusTemporaryRedirect)
				case "server-error":
					w.WriteHeader(http.StatusServiceUnavailable)
					fmt.Fprint(w, "unavailable")
				case "invalid-json":
					fmt.Fprint(w, "<html>error</html>")
				case "missing-id":
					fmt.Fprint(w, `{"code":"00000","status":0,"data":{}}`)
				case "missing-code":
					fmt.Fprint(w, `{}`)
				case "expired":
					fmt.Fprint(w, `{"code":"10001","status":1,"data":{}}`)
				case "quota":
					fmt.Fprint(w, `{"code":"500","status":1,"msg":"50000","data":{}}`)
				}
			}))
			client.credentialProvider = CredentialProviderFunc(func(context.Context) (string, string, error) { credentials.Add(1); return "u", "p", nil })
			_, err := client.BuildBacktest(context.Background(), "edit", testBuildOptions())
			if err == nil {
				t.Fatal("missing error")
			}
			if writes.Load() != 1 || credentials.Load() != 0 {
				t.Fatalf("write replayed: writes=%d credentials=%d", writes.Load(), credentials.Load())
			}
			switch mode {
			case "expired":
				if !errors.Is(err, ErrSessionExpired) {
					t.Fatal(err)
				}
			case "quota":
				var api *APIError
				if !errors.As(err, &api) || api.Message != "50000" || errors.Is(err, ErrSubmissionUnknown) {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, ErrSubmissionUnknown) {
					t.Fatalf("want uncertain outcome: %v", err)
				}
			}
		})
	}
}

func TestBuildValidationAndMissingTokenDoNotSubmit(t *testing.T) {
	var writes atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, strings.Replace(testEditorHTML, `window.tokenData`, `window.unrelated`, 1))
	}))
	if _, err := client.BuildBacktest(context.Background(), "edit", BuildBacktestOptions{}); err == nil {
		t.Error("invalid parameters accepted")
	}
	if _, err := client.BuildBacktest(context.Background(), "edit", testBuildOptions()); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("missing token: %v", err)
	}
	if writes.Load() != 0 {
		t.Fatal("invalid build reached upstream")
	}
}

func TestCancelBacktestResolvesDetailID(t *testing.T) {
	var writes atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/algorithm/backtest/detail" {
			if r.URL.Query().Get("backtestId") != "detail" {
				t.Error("wrong detail ID")
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script>window.tokenData={name:"token",value:"fresh-token"}</script><input id="backtestId" value="fresh-run">`)
			return
		}
		writes.Add(1)
		r.ParseForm()
		if r.URL.Path != "/algorithm/index/cancel" || r.PostForm.Get("backtestId") != "fresh-run" || r.PostForm.Get("token") != "fresh-token" {
			t.Error("incorrect cancel form")
		}
		fmt.Fprint(w, `{"code":"00000","status":"0","data":null}`)
	}))
	if err := client.CancelBacktest(context.Background(), "detail"); err != nil || writes.Load() != 1 {
		t.Fatalf("cancel = %v", err)
	}
}

func TestBacktestPendingDataIsNotACompletedEmptyHistory(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := `{"risk":false}`
		switch r.URL.Path {
		case "/algorithm/backtest/stats":
			data = `[ ]`
		case "/algorithm/backtest/statsList":
			data = `[{"backtestId":"run","stat":[]}]`
		case "/algorithm/backtest/result":
			data = `{"state":"0","result":0,"userRecord":null}`
		case "/algorithm/backtest/positionInfo":
			data = `{"status":"1","position":[],"max":false}`
		}
		fmt.Fprintf(w, `{"code":"00000","status":"0","data":%s}`, data)
	}))
	ctx := context.Background()
	if _, err := client.BacktestStats(ctx, "run"); !errors.Is(err, ErrBacktestNotReady) {
		t.Fatal(err)
	}
	if _, err := client.BacktestRisk(ctx, "run"); !errors.Is(err, ErrBacktestNotReady) {
		t.Fatal(err)
	}
	batch, err := client.BacktestBatchStats(ctx, "history", "run")
	if err != nil || len(batch) != 1 || len(batch[0].Stats) != 0 {
		t.Fatalf("batch=%+v %v", batch, err)
	}
	result, err := client.BacktestResultPage(ctx, "run", BacktestResultOptions{})
	if err != nil || result.Done {
		t.Fatalf("result=%+v %v", result, err)
	}
	positions, err := client.BacktestPositionPage(ctx, "run", BacktestCursor{})
	if err != nil || positions.Done {
		t.Fatalf("positions=%+v %v", positions, err)
	}
}

func TestBacktestCalendarAndUsage(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/algorithm/backtest/tradeDays" {
			if r.URL.Query().Get("startDay") != "2026-01-05" || r.URL.Query().Get("endDay") != "2026-01-06" {
				t.Error("wrong calendar range")
			}
			fmt.Fprint(w, `{"code":"00000","status":0,"data":[1767542400,1767628800]}`)
		} else {
			fmt.Fprint(w, `{"code":"00000","status":0,"data":{"running":[],"done":[],"duration":{"used":"1.5","free":10}}}`)
		}
	}))
	o := testBuildOptions()
	days, err := client.BacktestTradeDays(context.Background(), o.StartTime, o.EndTime)
	if err != nil || len(days) != 2 || days[0] > 1000000000000 {
		t.Fatalf("days=%v %v", days, err)
	}
	usage, err := client.BacktestUsage(context.Background())
	if err != nil || usage.Duration.Used.Value != 1.5 {
		t.Fatalf("usage=%+v %v", usage, err)
	}
}
