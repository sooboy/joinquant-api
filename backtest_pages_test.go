package joinquant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestAlgorithmPagesKeepIdentifiersAndFoldersSeparate(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/algorithm/index/list" || r.URL.Query().Get("ajax") != "" || r.URL.Query().Get("fId") != "parent" || r.Header.Get("X-Requested-With") != "" {
			t.Errorf("incorrect HTML request")
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<table id="algo_table"><tbody>
<tr class="algorithm_list"><td _fId="folder"></td><td><a href="/algorithm/index/list?fId=folder">目录</a></td><td>文件夹</td><td>2026-01-01</td><td></td><td></td></tr>
<tr class="algorithm_list"><td _algorithmId="operation"></td><td><a href="/algorithm/index/edit?algorithmId=edit">策略</a></td><td>Code</td><td>2026-01-01</td><td>0</td><td><a href="/algorithm/backtest/list?algorithmId=history">2</a></td></tr>
</tbody></table><ul><li class="next"><a href="/algorithm/index/list?fId=parent&amp;page=2">下一页</a></li></ul>`)
	}))
	p, err := client.ListAlgorithms(context.Background(), AlgorithmListOptions{FolderID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	if p.NextPage != 2 || len(p.Folders) != 1 || len(p.Algorithms) != 1 {
		t.Fatalf("page=%+v", p)
	}
	a := p.Algorithms[0]
	if a.OperationID != "operation" || a.EditID != "edit" || a.HistoryID != "history" || a.BacktestCount != 2 {
		t.Fatalf("algorithm=%+v", a)
	}
}

func TestAlgorithmLastPageUsesHiddenNextLink(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<table id="algo_table"><tbody></tbody></table><ul class="pagination"><li class="next nodisplay"><a href="/algorithm/index/list?page=3">下一页</a></li></ul>`)
	}))
	p, err := c.ListAlgorithms(context.Background(), AlgorithmListOptions{Page: 3})
	if err != nil || p.NextPage != 0 {
		t.Fatalf("last page was not terminal: %+v %v", p, err)
	}
}

func TestAlgorithmSourcePreservesEditorText(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/algorithm/index/edit" || r.URL.Query().Get("algorithmId") != "editor" {
			t.Fatal("source read used wrong endpoint or identifier")
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<input id=\"algorithmId\" value=\"operation\"><textarea id=\"code\">def initialize(context):\n    g.test = 1 &lt; 2\n</textarea>")
	}))
	source, err := c.AlgorithmSource(context.Background(), "editor")
	if err != nil || source != "def initialize(context):\n    g.test = 1 < 2\n" {
		t.Fatalf("source changed: %q %v", source, err)
	}
}

func TestBacktestHistoryAndDetailIdentifiers(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/algorithm/backtest/list" {
			if r.URL.Query().Get("algorithmId") != "history" {
				t.Error("wrong history ID")
			}
			fmt.Fprint(w, `<table id="backtest-feed-table"><tr class="backtest-tr" _backtestId="list-run" _backtestId2="detail" _status="2" _idx="1">
<td><input class="source-code" _backtestId="list-source"></td><td>1</td><td><span class="backtest-name">策略</span></td>
<td class="backtest-list__td_create-time">2026-01-01<br>12:00:00</td><td>2026-01-01<br>- 2026-01-06</td>
<td class="backtest-list__td_base-capital hidden">100000</td><td>1秒</td><td>每天</td><td class="backtest-list__td_python-version hidden">Python3</td>
</tr></table>`)
			return
		}
		if r.URL.Query().Get("backtestId") != "detail" {
			t.Error("wrong detail ID")
		}
		fmt.Fprint(w, `<input id="backtestId" value="detail-run"><input id="algorithmId" value="detail-algorithm">
<textarea id="code" _backtestId="detail-source"></textarea><span id="title-box">策略</span>
<span id="startDate">2026-01-01</span><span id="endDate">2026-01-06</span><span id="baseCapital">￥100000</span>
<span id="frequency" value="day">每天</span><input id="tradePyVersion" value="3">`)
	}))
	h, err := client.ListBacktests(context.Background(), "history", BacktestListOptions{})
	if err != nil || len(h.Entries) != 1 {
		t.Fatalf("history=%+v %v", h, err)
	}
	b := h.Entries[0]
	if b.ListRunID != "list-run" || b.SourceID != "list-source" || b.InitialCapital.Value != 100000 {
		t.Fatalf("entry=%+v", b)
	}
	d, err := client.Backtest(context.Background(), b.DetailID)
	if err != nil || d.RunID != "detail-run" || d.SourceID != "detail-source" || d.Frequency != BacktestDaily || d.InitialCapital.Value != 100000 {
		t.Fatalf("detail=%+v %v", d, err)
	}
}

func TestListAllBacktestsUsesAccountEndpoint(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/algorithm/backtest/items" || r.URL.Query().Has("algorithmId") || r.URL.Query().Get("limit") != "1" {
			t.Error("wrong account list query")
		}
		fmt.Fprint(w, `{"code":"00000","status":"0","data":{"page":"1","total":"2","keyword":"","list":[{"backtestId":"detail","name":"策略","baseCapital":"100000","frequency":"每天","status":"2","version":"3"}]}}`)
	}))
	p, err := client.ListAllBacktests(context.Background(), AllBacktestOptions{Limit: 1})
	if err != nil || p.NextPage != 2 || p.Entries[0].InitialCapital.Value != 100000 {
		t.Fatalf("page=%+v %v", p, err)
	}
}

func TestBacktestHTMLChangesDoNotBecomeEmptyResults(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html>unexpected page</html>`)
	}))
	ctx := context.Background()
	if _, err := client.ListAlgorithms(ctx, AlgorithmListOptions{}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal(err)
	}
	if _, err := client.ListBacktests(ctx, "history", BacktestListOptions{}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal(err)
	}
	if _, err := client.Backtest(ctx, "detail"); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal(err)
	}
}
