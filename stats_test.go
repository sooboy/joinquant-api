package joinquant

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestStatsLastFloat(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, `{"data":{"stat":{"max_drawdown":{"time":[1],"value":[0.125]}},"state":"1","offset":"0","count":1},"status":"0","code":"00000","msg":""}`)
	}))
	stats, err := client.Stats(context.Background(), "run-id")
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	value, err := stats.Metrics["max_drawdown"].LastFloat()
	if err != nil || value != 0.125 {
		t.Fatalf("LastFloat() = %v, %v", value, err)
	}
}
