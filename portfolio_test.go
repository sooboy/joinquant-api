package joinquant

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestPortfolioAndPositionParsing(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("backtestId") != "run-id" || query.Get("date") != "2026-09-01" || query.Get("isForward") != "1" {
			t.Errorf("query = %v", query)
		}
		fmt.Fprint(response, `{
  "data": {
    "position": [{
      "security":"股票","stock":"示例股票(000001.XSHE)","side":"long",
      "amount":"1,200股","closeableAmount":"1,000股","price":"10.00",
      "value":12000,"gain":200,"avgCost":"9.80","holdCost":"9.80",
      "margin":0,"dailyGains":100,"todayAmount":"200股","gainPercent":0.02,
      "gainPercentStr":"2.00%","time":"2026-09-01","totalValue":50000,
      "dailyGainsPercent":0.01,"todayGainsPercent":0.01,"truePrice":"10",
      "trueAvgCost":"9.8","trueAmount":"<span>1,200股</span>",
      "trueGainPercent":0.02,"positionPersent":"24.00%"
    }],
    "cash":38000,"totalValue":50000,"isLimit":false
  },
  "status":"0","code":"00000","msg":""
}`)
	}))

	date := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	portfolio, err := client.Portfolio(context.Background(), "run-id", date)
	if err != nil {
		t.Fatalf("Portfolio() error = %v", err)
	}
	quantity, err := portfolio.Positions[0].Quantity()
	if err != nil || portfolio.Positions[0].Code() != "000001.XSHE" || quantity != 1200 {
		t.Fatalf("position = %#v, quantity=%v, err=%v", portfolio.Positions[0], quantity, err)
	}
}

func TestParseDisplayNumbers(t *testing.T) {
	value, err := ParseNumber(`<span class="sell">-1,200股</span>`)
	if err != nil || value != -1200 {
		t.Fatalf("ParseNumber() = %v, %v", value, err)
	}
	percent, err := ParsePercent("24.50%")
	if err != nil || math.Abs(percent-0.245) > 1e-9 {
		t.Fatalf("ParsePercent() = %v, %v", percent, err)
	}
}
