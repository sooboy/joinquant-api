package joinquant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func backtestQuery(id string) (url.Values, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("backtest ID is required")
	}
	return url.Values{"backtestId": {id}}, nil
}

func backtestProtocol(message string) error {
	return fmt.Errorf("%w: backtest %s", ErrInvalidResponse, message)
}

func (c *Client) BacktestStats(ctx context.Context, runID string) (BacktestStats, error) {
	q, err := backtestQuery(runID)
	if err != nil {
		return nil, err
	}
	s, err := getEnvelope[BacktestStats](ctx, c, "/algorithm/backtest/stats", q)
	if err == nil && len(s) == 0 {
		return nil, ErrBacktestNotReady
	}
	return s, err
}

type BacktestBatchStat struct {
	BacktestID string        `json:"backtestId"`
	Stats      BacktestStats `json:"stat"`
	UsedTime   string        `json:"usedSec"`
}

func (c *Client) BacktestBatchStats(ctx context.Context, historyAlgorithmID string, listRunIDs ...string) ([]BacktestBatchStat, error) {
	if len(listRunIDs) == 0 {
		return []BacktestBatchStat{}, nil
	}
	if strings.TrimSpace(historyAlgorithmID) == "" {
		return nil, fmt.Errorf("history algorithm ID is required")
	}
	for _, id := range listRunIDs {
		if strings.TrimSpace(id) == "" || strings.Contains(id, ",") {
			return nil, fmt.Errorf("invalid list run ID")
		}
	}
	q := url.Values{"algorithmId": {historyAlgorithmID}, "backtestIds": {strings.Join(listRunIDs, ",")}}
	// Unlike the other verified queries, statsList returns code 20000 when
	// called without a same-site Referer, even with a valid session and IDs.
	referer := c.backtestPageURL("/algorithm/backtest/list", url.Values{"algorithmId": {historyAlgorithmID}})
	result, err := getEnvelopeHeaders[[]BacktestBatchStat](ctx, c, "/algorithm/backtest/statsList", q, http.Header{"Referer": {referer}})
	if err == nil && result == nil {
		return nil, backtestProtocol("missing batch stats")
	}
	return result, err
}

func (c *Client) BacktestResultPage(ctx context.Context, runID string, options BacktestResultOptions) (*BacktestResultPage, error) {
	q, err := backtestQuery(runID)
	if err != nil {
		return nil, err
	}
	if options.Offset < 0 || options.UserRecordOffset < 0 {
		return nil, fmt.Errorf("negative result offset")
	}
	q.Set("offset", strconv.Itoa(options.Offset))
	q.Set("userRecordOffset", strconv.Itoa(options.UserRecordOffset))
	var wire struct {
		State      BacktestState             `json:"state"`
		Result     json.RawMessage           `json:"result"`
		UserRecord map[string]BacktestSeries `json:"userRecord"`
	}
	wire, err = getEnvelope[struct {
		State      BacktestState             `json:"state"`
		Result     json.RawMessage           `json:"result"`
		UserRecord map[string]BacktestSeries `json:"userRecord"`
	}](ctx, c, "/algorithm/backtest/result", q)
	if err != nil {
		return nil, err
	}
	if wire.State == "" {
		return nil, backtestProtocol("missing result state")
	}
	p := &BacktestResultPage{State: wire.State, UserRecord: wire.UserRecord, NextOffset: options.Offset}
	raw := bytes.TrimSpace(wire.Result)
	empty := len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("0"))
	if empty {
		if wire.State == BacktestCompleted {
			return nil, backtestProtocol("completed result missing")
		}
	} else {
		if err := json.Unmarshal(raw, &p.Result); err != nil {
			return nil, backtestProtocol("invalid result chunk")
		}
		r := p.Result
		if r == nil || int(r.Offset) != options.Offset || r.Count < 0 {
			return nil, backtestProtocol("unexpected result cursor")
		}
		for _, series := range []BacktestSeries{r.Benchmark, r.OverallReturn} {
			if series.Time == nil || series.Value == nil || len(series.Time) != int(r.Count) || len(series.Value) != int(r.Count) {
				return nil, backtestProtocol("curve length does not match count")
			}
		}
		for _, series := range []BacktestSeries{r.Gains.Earn, r.Gains.Lose, r.Orders.Buy, r.Orders.Sell} {
			if len(series.Time) != len(series.Value) || (len(series.Time) != 0 && len(series.Time) != int(r.Count)) {
				return nil, backtestProtocol("auxiliary curve length mismatch")
			}
		}
		p.NextOffset = int(r.Offset) + int(r.Count)
		if p.NextOffset < options.Offset {
			return nil, backtestProtocol("result cursor overflow")
		}
	}
	customCount := 0
	for _, series := range p.UserRecord {
		if len(series.Time) != len(series.Value) {
			return nil, backtestProtocol("custom record length mismatch")
		}
		customCount += len(series.Time)
	}
	p.Done = p.State.Terminal() && p.NextOffset == options.Offset && customCount == 0
	return p, nil
}

func detailPageQuery(runID string, cursor BacktestCursor) (url.Values, error) {
	q, err := backtestQuery(runID)
	if err != nil {
		return nil, err
	}
	if cursor.Offset < 0 {
		return nil, fmt.Errorf("negative detail offset")
	}
	if cursor.Offset > 0 && cursor.DateOffset == "" {
		return nil, fmt.Errorf("date offset is required after the first page")
	}
	if cursor.DateOffset != "" {
		if _, err := time.Parse("2006-01-02", cursor.DateOffset); err != nil {
			return nil, fmt.Errorf("invalid date offset")
		}
		q.Set("dateOffset", cursor.DateOffset)
	}
	if cursor.Offset > 0 {
		q.Set("offset", strconv.Itoa(cursor.Offset))
	}
	return q, nil
}

func nextBacktestCursor(cursor BacktestCursor, count int, lastDate string) (BacktestCursor, error) {
	if count == 0 {
		return cursor, nil
	}
	if _, err := time.Parse("2006-01-02", lastDate); err != nil {
		return cursor, backtestProtocol("invalid last row date")
	}
	if cursor.DateOffset != "" && lastDate < cursor.DateOffset {
		return cursor, backtestProtocol("date cursor moved backwards")
	}
	next := BacktestCursor{Offset: cursor.Offset + count, DateOffset: lastDate}
	if next.Offset <= cursor.Offset {
		return cursor, backtestProtocol("detail cursor overflow")
	}
	return next, nil
}

// BacktestPositionPage returns the page together with ErrTruncated when Max is
// true, including when the server returns an empty page at its display limit.
// Done means a terminal task returned an empty, untruncated page.
func (c *Client) BacktestPositionPage(ctx context.Context, runID string, cursor BacktestCursor) (*BacktestPositionPage, error) {
	q, err := detailPageQuery(runID, cursor)
	if err != nil {
		return nil, err
	}
	p, err := getEnvelope[*BacktestPositionPage](ctx, c, "/algorithm/backtest/positionInfo", q)
	if err != nil {
		return nil, err
	}
	if p == nil || p.State == "" || p.Positions == nil {
		return nil, backtestProtocol("missing position page fields")
	}
	date := ""
	if len(p.Positions) > 0 {
		date = p.Positions[len(p.Positions)-1].Date
	}
	p.NextCursor, err = nextBacktestCursor(cursor, len(p.Positions), date)
	if err != nil {
		return nil, err
	}
	p.Done = p.State.Terminal() && len(p.Positions) == 0 && !p.Max
	if p.Max {
		return p, ErrTruncated
	}
	return p, nil
}

func (c *Client) BacktestTransactionPage(ctx context.Context, runID string, cursor BacktestCursor) (*BacktestTransactionPage, error) {
	q, err := detailPageQuery(runID, cursor)
	if err != nil {
		return nil, err
	}
	p, err := getEnvelope[*BacktestTransactionPage](ctx, c, "/algorithm/backtest/transactionInfo", q)
	if err != nil {
		return nil, err
	}
	if p == nil || p.State == "" || p.Transactions == nil {
		return nil, backtestProtocol("missing transaction page fields")
	}
	date := ""
	if len(p.Transactions) > 0 {
		last := p.Transactions[len(p.Transactions)-1]
		date = last.TradeDate
		if date == "" {
			date = last.Date
		}
	}
	p.NextCursor, err = nextBacktestCursor(cursor, len(p.Transactions), date)
	if err != nil {
		return nil, err
	}
	p.Done = p.State.Terminal() && len(p.Transactions) == 0 && !p.Max
	if p.Max {
		return p, ErrTruncated
	}
	return p, nil
}

func (c *Client) BacktestLogs(ctx context.Context, runID string, offset int) (*BacktestLogPage, error) {
	return c.backtestLogs(ctx, runID, offset, false)
}

func (c *Client) BacktestErrors(ctx context.Context, runID string, offset int) (*BacktestLogPage, error) {
	return c.backtestLogs(ctx, runID, offset, true)
}

func (c *Client) backtestLogs(ctx context.Context, runID string, offset int, errorLog bool) (*BacktestLogPage, error) {
	q, err := backtestQuery(runID)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("negative log offset")
	}
	q.Set("offset", strconv.Itoa(offset))
	path := "/algorithm/backtest/log"
	if errorLog {
		path = "/algorithm/backtest/error"
	}
	p, err := getEnvelope[*BacktestLogPage](ctx, c, path, q)
	if err != nil {
		return nil, err
	}
	if p == nil || p.State == "" || p.Lines == nil {
		return nil, backtestProtocol("missing log fields")
	}
	if !errorLog && int(p.Offset) != offset {
		return nil, backtestProtocol("unexpected log offset")
	}
	// The error endpoint does not echo the offset.
	if errorLog {
		p.Offset = IntValue(offset)
	}
	p.NextOffset = offset
	for _, line := range p.Lines {
		if errorLog || line != "" {
			p.NextOffset++
		}
	}
	if p.NextOffset < offset || (len(p.Lines) > 0 && p.NextOffset == offset) {
		return nil, backtestProtocol("log cursor did not advance")
	}
	p.Done = p.State.Terminal() && len(p.Lines) == 0 && !p.Max
	if p.Max {
		return p, ErrTruncated
	}
	return p, nil
}

func (c *Client) BacktestSource(ctx context.Context, sourceID string) (string, error) {
	q, err := backtestQuery(sourceID)
	if err != nil {
		return "", err
	}
	data, err := getEnvelope[struct {
		Source *string `json:"source"`
	}](ctx, c, "/algorithm/backtest/source", q)
	if err != nil {
		return "", err
	}
	if data.Source == nil {
		return "", backtestProtocol("source missing")
	}
	return *data.Source, nil
}

func (c *Client) BacktestRuntime(ctx context.Context, runID string) (*BacktestRuntime, error) {
	q, err := backtestQuery(runID)
	if err != nil {
		return nil, err
	}
	data, err := getEnvelope[*BacktestRuntime](ctx, c, "/algorithm/backtest/runTimeInfo", q)
	if err == nil && (data == nil || data.State == "") {
		return nil, backtestProtocol("runtime state missing")
	}
	return data, err
}

func (c *Client) BacktestProfile(ctx context.Context, runID string) (*BacktestProfile, error) {
	q, err := backtestQuery(runID)
	if err != nil {
		return nil, err
	}
	data, err := getEnvelope[*BacktestProfile](ctx, c, "/algorithm/backtest/profile", q)
	if err == nil && (data == nil || data.State == "") {
		return nil, backtestProtocol("profile state missing")
	}
	return data, err
}

func (c *Client) BacktestRisk(ctx context.Context, runID string) (BacktestRisk, error) {
	q, err := backtestQuery(runID)
	if err != nil {
		return nil, err
	}
	data, err := getEnvelope[struct {
		Risk json.RawMessage `json:"risk"`
	}](ctx, c, "/algorithm/backtest/risk", q)
	if err != nil {
		return nil, err
	}
	raw := bytes.TrimSpace(data.Risk)
	if bytes.Equal(raw, []byte("0")) || bytes.Equal(raw, []byte("false")) || bytes.Equal(raw, []byte("null")) {
		return nil, ErrBacktestNotReady
	}
	var result BacktestRisk
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: decode backtest risk: %v", ErrInvalidResponse, err)
	}
	return result, nil
}

// BacktestTradeDays returns Unix seconds, not the milliseconds used by curves.
func (c *Client) BacktestTradeDays(ctx context.Context, startDay, endDay time.Time) ([]int64, error) {
	if startDay.IsZero() || endDay.IsZero() || endDay.Before(startDay) {
		return nil, fmt.Errorf("valid trade day range is required")
	}
	q := url.Values{"startDay": {startDay.Format("2006-01-02")}, "endDay": {endDay.Format("2006-01-02")}}
	days, err := getEnvelope[[]int64](ctx, c, "/algorithm/backtest/tradeDays", q)
	if err == nil && days == nil {
		return nil, backtestProtocol("trade days missing")
	}
	return days, err
}

type BacktestUsage struct {
	Running  json.RawMessage `json:"running"`
	Done     json.RawMessage `json:"done"`
	Duration struct {
		Used OptionalNumber `json:"used"`
		Free OptionalNumber `json:"free"`
	} `json:"duration"`
}

func (c *Client) BacktestUsage(ctx context.Context) (*BacktestUsage, error) {
	usage, err := getEnvelope[*BacktestUsage](ctx, c, "/algorithm/index/statistics", nil)
	if err == nil && (usage == nil || !usage.Duration.Used.Valid || !usage.Duration.Free.Valid) {
		return nil, backtestProtocol("usage duration missing")
	}
	return usage, err
}
