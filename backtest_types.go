package joinquant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var groupedBacktestNumber = regexp.MustCompile(`^[+-]?[0-9]{1,3}(,[0-9]{3})+(\.[0-9]+)?$`)

// OptionalNumber distinguishes zero from missing data. Percent strings are
// converted to ratios; ordinary numbers keep their original units.
type OptionalNumber struct {
	Value float64
	Valid bool
}

func (n *OptionalNumber) UnmarshalJSON(raw []byte) error {
	*n = OptionalNumber{}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value StringValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	s := strings.TrimSpace(string(value))
	if s == "" || s == "N/A" || s == "-" || s == "--" {
		return nil
	}
	percent := strings.HasSuffix(s, "%")
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	if strings.Contains(s, ",") {
		// Only accept actual grouped decimal numbers, not arbitrary text.
		if !groupedBacktestNumber.MatchString(s) {
			return fmt.Errorf("invalid grouped number")
		}
		s = strings.ReplaceAll(s, ",", "")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("invalid finite number")
	}
	if percent {
		v /= 100
	}
	n.Value, n.Valid = v, true
	return nil
}

func (n OptionalNumber) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}

// IntValue accepts integer JSON numbers and integer strings, rejecting
// fractions and overflow. It is used for the upstream's inconsistent cursors.
type IntValue int

func (n *IntValue) UnmarshalJSON(raw []byte) error {
	var s StringValue
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	v, err := strconv.Atoi(string(s))
	if err != nil {
		return fmt.Errorf("expected integer")
	}
	*n = IntValue(v)
	return nil
}

type BacktestState string

const (
	BacktestQueued    BacktestState = "0"
	BacktestRunning   BacktestState = "1"
	BacktestCompleted BacktestState = "2"
	BacktestFailed    BacktestState = "3"
	BacktestCancelled BacktestState = "4"
)

func (s *BacktestState) UnmarshalJSON(raw []byte) error {
	var value StringValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*s = BacktestState(value)
	return nil
}

// Terminal does not treat unknown states as completed tasks.
func (s BacktestState) Terminal() bool {
	return s == BacktestCompleted || s == BacktestFailed || s == BacktestCancelled
}

type BacktestFrequency string

const (
	BacktestDaily  BacktestFrequency = "day"
	BacktestMinute BacktestFrequency = "minute"
	BacktestTick   BacktestFrequency = "tick"
)

// BacktestStats contains scalar metrics, unlike the live Stats time series.
// Raw values preserve date ranges, percentages, missing values and new fields.
type BacktestStats map[string]json.RawMessage

func (s *BacktestStats) UnmarshalJSON(raw []byte) error {
	// statsList can return [] while metrics are still being generated.
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil && len(items) == 0 {
			*s = nil
			return nil
		}
	}
	type plain BacktestStats
	var result plain
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	*s = BacktestStats(result)
	return nil
}

func (s BacktestStats) Number(name string) (OptionalNumber, error) {
	var value OptionalNumber
	raw, ok := s[name]
	if !ok {
		return value, fmt.Errorf("metric %q not found", name)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("metric %q: %w", name, err)
	}
	return value, nil
}

type BacktestSeries struct {
	Time  []int64          `json:"time"` // Unix milliseconds.
	Value []OptionalNumber `json:"value"`
}

type BacktestResultChunk struct {
	Offset        IntValue       `json:"offset"`
	Count         IntValue       `json:"count"`
	Benchmark     BacktestSeries `json:"benchmark"`
	OverallReturn BacktestSeries `json:"overallReturn"`
	Gains         struct {
		Earn BacktestSeries `json:"earn"`
		Lose BacktestSeries `json:"lose"`
	} `json:"gains"`
	Orders struct {
		Buy  BacktestSeries `json:"buy"`
		Sell BacktestSeries `json:"sell"`
	} `json:"orders"`
}

func (r *BacktestResultChunk) UnmarshalJSON(raw []byte) error {
	type plain BacktestResultChunk
	var result plain
	wire := struct {
		*plain
		Offset *IntValue `json:"offset"`
		Count  *IntValue `json:"count"`
	}{plain: &result}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if wire.Offset == nil || wire.Count == nil {
		return fmt.Errorf("result offset/count missing")
	}
	result.Offset, result.Count = *wire.Offset, *wire.Count
	*r = BacktestResultChunk(result)
	return nil
}

// BacktestResultPage preserves curve returns in percentage points (12 means
// 12%), while BacktestStats.Number("algorithm_return") returns a ratio.
// UserRecordOffset is independent of the curve cursor; callers must manage it
// for strategies that emit custom records.
type BacktestResultPage struct {
	State      BacktestState             `json:"state"`
	Result     *BacktestResultChunk      `json:"result"`
	UserRecord map[string]BacktestSeries `json:"userRecord"`
	NextOffset int                       `json:"-"`
	Done       bool                      `json:"-"`
}

type BacktestResultOptions struct {
	Offset           int
	UserRecordOffset int
}

// BacktestCursor is computed from all returned rows, including cash rows and
// zero-quantity transactions. The zero value requests the first page.
type BacktestCursor struct {
	Offset     int
	DateOffset string
}

type BacktestPosition struct {
	Date                string         `json:"date"`
	Time                string         `json:"time"`
	Security            string         `json:"security"`
	Stock               string         `json:"stock"`
	Side                string         `json:"side"`
	Amount              StringValue    `json:"amount"`
	CloseableAmount     StringValue    `json:"closeableAmount"`
	Price               StringValue    `json:"price"`
	Value               OptionalNumber `json:"value"`
	Gain                OptionalNumber `json:"gain"`
	AverageCost         StringValue    `json:"avgCost"`
	HoldingCost         StringValue    `json:"holdCost"`
	Margin              OptionalNumber `json:"margin"`
	DailyGains          OptionalNumber `json:"dailyGains"`
	TodayAmount         StringValue    `json:"todayAmount"`
	GainPercent         OptionalNumber `json:"gainPercent"`
	GainPercentText     string         `json:"gainPercentStr"`
	TotalValue          OptionalNumber `json:"totalValue"`
	DailyGainsPercent   OptionalNumber `json:"dailyGainsPercent"`
	TodayGainsPercent   OptionalNumber `json:"todayGainsPercent"`
	TruePrice           StringValue    `json:"truePrice"`
	TrueAverageCost     StringValue    `json:"trueAvgCost"`
	TrueAmount          StringValue    `json:"trueAmount"`
	TrueGainPercent     OptionalNumber `json:"trueGainPercent"`
	PositionPercentText string         `json:"positionPersent"`
}

func (p BacktestPosition) IsCash() bool {
	return strings.EqualFold(strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(p.Stock, ""))), "Cash")
}

func (p BacktestPosition) Code() string {
	if p.IsCash() {
		return ""
	}
	return (Position{Stock: p.Stock}).Code()
}

func (p BacktestPosition) Quantity() (float64, error) {
	if p.IsCash() {
		return 0, fmt.Errorf("cash row has no security quantity")
	}
	return (Position{Amount: string(p.Amount), TrueAmount: string(p.TrueAmount)}).Quantity()
}

type BacktestPositionPage struct {
	State      BacktestState      `json:"status"`
	Positions  []BacktestPosition `json:"position"`
	Max        bool               `json:"max"`
	NextCursor BacktestCursor     `json:"-"`
	Done       bool               `json:"-"`
}

type BacktestTransaction struct {
	Date               string         `json:"date"`
	TradeDate          string         `json:"tradeDate"`
	Time               string         `json:"time"`
	Security           string         `json:"security"`
	Stock              string         `json:"stock"`
	Transaction        string         `json:"transaction"`
	Type               string         `json:"type"`
	Amount             StringValue    `json:"amount"`
	Price              StringValue    `json:"price"`
	Total              StringValue    `json:"total"`
	Gains              OptionalNumber `json:"gains"`
	Commission         OptionalNumber `json:"commission"`
	Status             string         `json:"status"`
	OrderAmount        StringValue    `json:"orderAmount"`
	LimitPrice         StringValue    `json:"limitPrice"`
	MatchTime          string         `json:"matchTime"`
	TransactionPercent string         `json:"transactionPersent"`
	TrueOrderAmount    StringValue    `json:"trueOrderAmount"`
	TrueAmount         StringValue    `json:"trueAmount"`
	TrueLimitPrice     OptionalNumber `json:"trueLimitPrice"`
	TruePrice          OptionalNumber `json:"truePrice"`
}

func (t BacktestTransaction) Code() string {
	return (Transaction{Stock: t.Stock}).Code()
}

func (t BacktestTransaction) SignedFilledQuantity() (float64, error) {
	return (Transaction{Amount: string(t.Amount), TrueAmount: string(t.TrueAmount)}).SignedFilledQuantity()
}

// TotalAmount accepts the parenthesized amount used by the web display while
// keeping the original string in Total.
func (t BacktestTransaction) TotalAmount() (OptionalNumber, error) {
	s := strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(string(t.Total), "")))
	if start := strings.IndexByte(s, '('); start >= 0 {
		end := strings.LastIndexByte(s, ')')
		if end <= start {
			return OptionalNumber{}, fmt.Errorf("invalid transaction total")
		}
		s = s[start+1 : end]
	}
	raw, _ := json.Marshal(s)
	var n OptionalNumber
	err := json.Unmarshal(raw, &n)
	return n, err
}

type BacktestTransactionPage struct {
	State        BacktestState         `json:"status"`
	Transactions []BacktestTransaction `json:"transaction"`
	Max          bool                  `json:"max"`
	NextCursor   BacktestCursor        `json:"-"`
	Done         bool                  `json:"-"`
}

type BacktestLogPage struct {
	State      BacktestState `json:"state"`
	Lines      []string      `json:"logArr"`
	Offset     IntValue      `json:"offset"`
	Max        bool          `json:"max"`
	NextOffset int           `json:"-"`
	Done       bool          `json:"-"`
}

type BacktestProfile struct {
	State   BacktestState `json:"state"`
	Profile string        `json:"profile"`
}

type BacktestRuntime struct {
	State        BacktestState   `json:"status"`
	StartTime    string          `json:"startTime"`
	EndTime      string          `json:"endTime"`
	AddedAt      string          `json:"addTime"`
	RunStartTime string          `json:"runStartTime"`
	CancelTime   string          `json:"cancelTime"` // May be "0000-00-00 00:00:00".
	CurrentTime  string          `json:"currentTime"`
	NeedSeconds  OptionalNumber  `json:"needSeconds"`
	UntilNow     string          `json:"untilNowSec"`
	CreditsLog   json.RawMessage `json:"creditsLog"` // Upstream returns an array or object.
}

type BacktestRiskWindow struct {
	Date    string         `json:"date"`
	Month1  OptionalNumber `json:"1month"`
	Month3  OptionalNumber `json:"3month"`
	Month6  OptionalNumber `json:"6month"`
	Month12 OptionalNumber `json:"12month"`
}

// Keys retain upstream spellings, including sharp, algovolatility and maxdrawdown.
type BacktestRisk map[string][]BacktestRiskWindow
