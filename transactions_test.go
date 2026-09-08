package joinquant

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestTransactionsPreserveDirectionAndParseQuantity(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, `{
  "data":{"transaction":[{
    "date":"2026-09-01","time":"09:30:00","security":"股票",
    "stock":"示例股票(000001.XSHE)","transaction":"卖","type":"市价单",
    "amount":"<span>-500股</span>","price":"10.00","total":-5000,
    "gains":20,"commission":5,"status":"全部成交","orderAmount":"-500股",
    "limitPrice":"-","matchTime":"2026-09-01 09:30:00",
    "transactionPersent":"100%","trueOrderAmount":"-500股",
    "trueAmount":"<span>-500股</span>","trueLimitPrice":0,"truePrice":10
  }],"isLimit":false},"status":"0","code":"00000","msg":""
}`)
	}))

	date := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	page, err := client.Transactions(context.Background(), "run-id", date)
	if err != nil {
		t.Fatalf("Transactions() error = %v", err)
	}
	signed, err := page.Transactions[0].SignedFilledQuantity()
	if err != nil || signed != -500 {
		t.Fatalf("SignedFilledQuantity() = %v, %v", signed, err)
	}
	absolute, err := page.Transactions[0].FilledQuantity()
	if err != nil || absolute != 500 {
		t.Fatalf("FilledQuantity() = %v, %v", absolute, err)
	}
}
