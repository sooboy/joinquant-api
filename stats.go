package joinquant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type MetricSeries struct {
	Time  []int64           `json:"time"`
	Value []json.RawMessage `json:"value"`
}

func (m MetricSeries) LastFloat() (float64, error) {
	if len(m.Value) == 0 {
		return 0, fmt.Errorf("metric has no values")
	}
	value := m.Value[len(m.Value)-1]
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil {
		return strconv.ParseFloat(number.String(), 64)
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return strconv.ParseFloat(text, 64)
	}
	return 0, fmt.Errorf("metric value is not numeric: %s", string(value))
}

type Stats struct {
	Metrics map[string]MetricSeries `json:"stat"`
	State   StringValue             `json:"state"`
	Offset  StringValue             `json:"offset"`
	Count   int                     `json:"count"`
}

func (c *Client) Stats(ctx context.Context, runID string) (*Stats, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	query := url.Values{
		"offset":     {"-1"},
		"limit":      {"1"},
		"backtestId": {runID},
	}
	stats, err := getEnvelope[Stats](ctx, c, "/algorithm/live/stat", query)
	if err != nil {
		return nil, err
	}
	if stats.Metrics == nil {
		stats.Metrics = make(map[string]MetricSeries)
	}
	return &stats, nil
}

type StrategyStats struct {
	BacktestID string                  `json:"backtestId"`
	Metrics    map[string]MetricSeries `json:"stat"`
}

func (c *Client) BatchStats(ctx context.Context, runIDs ...string) ([]StrategyStats, error) {
	if len(runIDs) == 0 {
		return []StrategyStats{}, nil
	}
	query := url.Values{
		"offset":      {"-1"},
		"limit":       {"1"},
		"backtestIds": {strings.Join(runIDs, ",")},
	}
	result, err := getEnvelope[[]StrategyStats](ctx, c, "/algorithm/live/stats", query)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = make([]StrategyStats, 0)
	}
	return result, nil
}

type CurrentResult struct {
	BacktestID string          `json:"backtestId"`
	Result     json.RawMessage `json:"result"`
}

func (c *Client) CurrentResults(ctx context.Context, runIDs ...string) ([]CurrentResult, error) {
	if len(runIDs) == 0 {
		return []CurrentResult{}, nil
	}
	query := url.Values{"backtestId": {strings.Join(runIDs, ",")}}
	result, err := getEnvelope[[]CurrentResult](ctx, c, "/algorithm/backtest/currentResult", query)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = make([]CurrentResult, 0)
	}
	return result, nil
}
