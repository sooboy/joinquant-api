package joinquant

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Transaction struct {
	Date               string  `json:"date"`
	Time               string  `json:"time"`
	Security           string  `json:"security"`
	Stock              string  `json:"stock"`
	Transaction        string  `json:"transaction"`
	Type               string  `json:"type"`
	Amount             string  `json:"amount"`
	Price              string  `json:"price"`
	Total              float64 `json:"total"`
	Gains              float64 `json:"gains"`
	Commission         float64 `json:"commission"`
	Status             string  `json:"status"`
	OrderAmount        string  `json:"orderAmount"`
	LimitPrice         string  `json:"limitPrice"`
	MatchTime          string  `json:"matchTime"`
	TransactionPercent string  `json:"transactionPersent"`
	TrueOrderAmount    string  `json:"trueOrderAmount"`
	TrueAmount         string  `json:"trueAmount"`
	TrueLimitPrice     float64 `json:"trueLimitPrice"`
	TruePrice          float64 `json:"truePrice"`
}

type TransactionPage struct {
	Transactions []Transaction `json:"transaction"`
	IsLimit      bool          `json:"isLimit"`
}

type TransactionOptions struct {
	Limit     int
	SortField string
	SortOrder string
}

func (c *Client) Transactions(ctx context.Context, runID string, date time.Time) (*TransactionPage, error) {
	return c.TransactionsWithOptions(ctx, runID, date, TransactionOptions{})
}

func (c *Client) TransactionsWithOptions(ctx context.Context, runID string, date time.Time, options TransactionOptions) (*TransactionPage, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	if date.IsZero() {
		return nil, fmt.Errorf("transaction date is required")
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 200
	}
	query := url.Values{
		"limit":      {strconv.Itoa(limit)},
		"backtestId": {runID},
		"date":       {date.Format("2006-01-02")},
		"field":      {options.SortField},
		"order":      {options.SortOrder},
	}
	page, err := getEnvelope[TransactionPage](ctx, c, "/algorithm/live/transactionDetail", query)
	if err != nil {
		return nil, err
	}
	if page.Transactions == nil {
		page.Transactions = make([]Transaction, 0)
	}
	return &page, nil
}

func (t Transaction) Code() string {
	start := strings.LastIndexByte(t.Stock, '(')
	end := strings.LastIndexByte(t.Stock, ')')
	if start >= 0 && end > start {
		return strings.TrimSpace(t.Stock[start+1 : end])
	}
	return strings.TrimSpace(t.Stock)
}

func (t Transaction) SignedFilledQuantity() (float64, error) {
	value := t.TrueAmount
	if strings.TrimSpace(value) == "" {
		value = t.Amount
	}
	return ParseNumber(value)
}

func (t Transaction) FilledQuantity() (float64, error) {
	value, err := t.SignedFilledQuantity()
	return math.Abs(value), err
}

func (t Transaction) SignedOrderQuantity() (float64, error) {
	value := t.TrueOrderAmount
	if strings.TrimSpace(value) == "" {
		value = t.OrderAmount
	}
	return ParseNumber(value)
}
