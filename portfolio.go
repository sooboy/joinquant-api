package joinquant

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	htmlTagPattern = regexp.MustCompile(`<[^>]*>`)
	numberPattern  = regexp.MustCompile(`[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?`)
)

type Position struct {
	Security            string  `json:"security"`
	Stock               string  `json:"stock"`
	Side                string  `json:"side"`
	Amount              string  `json:"amount"`
	CloseableAmount     string  `json:"closeableAmount"`
	Price               string  `json:"price"`
	Value               float64 `json:"value"`
	Gain                float64 `json:"gain"`
	AverageCost         string  `json:"avgCost"`
	HoldingCost         string  `json:"holdCost"`
	Margin              float64 `json:"margin"`
	DailyGains          float64 `json:"dailyGains"`
	TodayAmount         string  `json:"todayAmount"`
	GainPercent         float64 `json:"gainPercent"`
	GainPercentText     string  `json:"gainPercentStr"`
	Time                string  `json:"time"`
	TotalValue          float64 `json:"totalValue"`
	DailyGainsPercent   float64 `json:"dailyGainsPercent"`
	TodayGainsPercent   float64 `json:"todayGainsPercent"`
	TruePrice           string  `json:"truePrice"`
	TrueAverageCost     string  `json:"trueAvgCost"`
	TrueAmount          string  `json:"trueAmount"`
	TrueGainPercent     float64 `json:"trueGainPercent"`
	PositionPercentText string  `json:"positionPersent"`
}

type Portfolio struct {
	Positions  []Position `json:"position"`
	Cash       float64    `json:"cash"`
	TotalValue float64    `json:"totalValue"`
	IsLimit    bool       `json:"isLimit"`
}

type PositionOptions struct {
	Limit     int
	IsForward bool
	SortField string
	SortOrder string
}

func (c *Client) Portfolio(ctx context.Context, runID string, date time.Time) (*Portfolio, error) {
	// The detail page uses isForward=1 for its initial/latest snapshot.
	return c.PortfolioWithOptions(ctx, runID, date, PositionOptions{IsForward: true})
}

func (c *Client) PortfolioWithOptions(ctx context.Context, runID string, date time.Time, options PositionOptions) (*Portfolio, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	if date.IsZero() {
		return nil, fmt.Errorf("portfolio date is required")
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 50
	}
	query := url.Values{
		"limit":      {strconv.Itoa(limit)},
		"backtestId": {runID},
		"date":       {date.Format("2006-01-02")},
		"isForward":  {"0"},
		"field":      {options.SortField},
		"order":      {options.SortOrder},
	}
	if options.IsForward {
		query.Set("isForward", "1")
	}
	portfolio, err := getEnvelope[Portfolio](ctx, c, "/algorithm/live/position", query)
	if err != nil {
		return nil, err
	}
	if portfolio.Positions == nil {
		portfolio.Positions = make([]Position, 0)
	}
	return &portfolio, nil
}

func (p Position) Code() string {
	start := strings.LastIndexByte(p.Stock, '(')
	end := strings.LastIndexByte(p.Stock, ')')
	if start >= 0 && end > start {
		return strings.TrimSpace(p.Stock[start+1 : end])
	}
	return strings.TrimSpace(p.Stock)
}

func (p Position) Name() string {
	if start := strings.LastIndexByte(p.Stock, '('); start > 0 {
		return strings.TrimSpace(p.Stock[:start])
	}
	return strings.TrimSpace(p.Stock)
}

func (p Position) Quantity() (float64, error) {
	value := p.TrueAmount
	if strings.TrimSpace(value) == "" {
		value = p.Amount
	}
	return ParseNumber(value)
}

func (p Position) CloseableQuantity() (float64, error) {
	return ParseNumber(p.CloseableAmount)
}

func (p Position) CurrentPrice() (float64, error) {
	value := p.TruePrice
	if strings.TrimSpace(value) == "" {
		value = p.Price
	}
	return ParseNumber(value)
}

func (p Position) ReportedWeight() (float64, error) {
	return ParsePercent(p.PositionPercentText)
}

// Weight calculates the position's fraction of total portfolio value. This is
// preferred over parsing PositionPercentText because it does not lose precision
// to the web UI's percentage formatting.
// ParseNumber parses JoinQuant display strings such as "1,200股", HTML-wrapped
// quantities, prices and plain JSON numeric text.
func ParseNumber(value string) (float64, error) {
	cleaned := html.UnescapeString(htmlTagPattern.ReplaceAllString(value, ""))
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	match := numberPattern.FindString(cleaned)
	if match == "" {
		return 0, fmt.Errorf("no number in %q", value)
	}
	result, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", value, err)
	}
	return result, nil
}

func ParsePercent(value string) (float64, error) {
	result, err := ParseNumber(value)
	if err != nil {
		return 0, err
	}
	if strings.Contains(value, "%") {
		result /= 100
	}
	return result, nil
}
