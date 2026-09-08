package joinquant

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestListStrategiesParsesThreeIdentifiers(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, exists := request.URL.Query()["ajax"]; exists {
			t.Errorf("HTML list must not send ajax")
		}
		if value := request.Header.Get("X-Requested-With"); value != "" {
			t.Errorf("HTML list X-Requested-With = %q, want empty", value)
		}
		if request.URL.Query().Get("process") != "1" {
			t.Errorf("process = %q", request.URL.Query().Get("process"))
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(response, `<!doctype html><html><body>
<table id="live_table"><tbody>
<tr _status="1" data-backtestspaceid="stable-space">
  <td class="backtest-checkbox" _backtestid="opaque-run"></td>
  <td class="name"><a href="/algorithm/live/index?backtestId=opaque-detail">测试策略</a></td>
  <td>频率 <span class="js-frequency">每天</span></td>
  <td>状态 实时进行中</td>
  <td>2026-09-01</td>
</tr>
</tbody></table></body></html>`)
	}))

	strategies, err := client.ListStrategies(context.Background(), RunningStrategies)
	if err != nil {
		t.Fatalf("ListStrategies() error = %v", err)
	}
	if len(strategies) != 1 {
		t.Fatalf("len = %d", len(strategies))
	}
	strategy := strategies[0]
	if strategy.SpaceID != "stable-space" || strategy.RunID != "opaque-run" || strategy.DetailID != "opaque-detail" {
		t.Fatalf("strategy IDs = %#v", strategy)
	}
	if strategy.Frequency != "每天" || strategy.Status != "实时进行中" {
		t.Fatalf("strategy labels = %#v", strategy)
	}
}
