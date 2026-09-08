package joinquant

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type BacktestMode int

const (
	FullBacktest BacktestMode = iota
	CompileBacktest
)

type BuildBacktestOptions struct {
	// Name optionally names a dedicated strategy at submission time.
	Name string
	// Source nil uses the current editor source. A non-nil source must be
	// nonempty. Building may also update the strategy's saved code/settings.
	Source         *string
	StartTime      time.Time
	EndTime        time.Time
	InitialCapital float64
	Frequency      BacktestFrequency
	Mode           BacktestMode
	// AllowCredit explicitly permits the upstream to consume account credits.
	AllowCredit bool
}

type BuildBacktestResult struct {
	AlgorithmID     string      `json:"algorithmId"`
	BacktestID      string      `json:"backtestId"` // Resolve through Client.Backtest.
	LocalBacktestID StringValue `json:"backtestId_"`
	TradeDays       []int64     `json:"tradeDays"` // Unix seconds, when returned.
}

// SubmissionError indicates an uncertain write outcome. Error text omits
// payloads and credentials; Unwrap preserves the underlying transport/protocol
// error. Reconcile the history before deciding whether to submit again.
type SubmissionError struct {
	Operation string
	Cause     error
}

func (e *SubmissionError) Error() string {
	return "joinquant " + e.Operation + ": submission outcome is unknown"
}

func (e *SubmissionError) Unwrap() error { return e.Cause }

func (e *SubmissionError) Is(target error) bool { return target == ErrSubmissionUnknown }

// BuildBacktest loads fresh editor fields/token and submits exactly once. It
// does not automatically retry after sending, including on login expiration.
// editID comes from Algorithm.EditID, not Algorithm.OperationID or HistoryID.
func (c *Client) BuildBacktest(ctx context.Context, editID string, options BuildBacktestOptions) (*BuildBacktestResult, error) {
	if err := validateBuildBacktest(editID, options); err != nil {
		return nil, err
	}
	doc, err := c.backtestHTML(ctx, "/algorithm/index/edit", url.Values{"algorithmId": {editID}})
	if err != nil {
		return nil, err
	}
	form, err := buildBacktestForm(doc, options)
	if err != nil {
		return nil, err
	}
	referer := c.backtestPageURL("/algorithm/index/edit", url.Values{"algorithmId": {editID}})
	result, err := postBacktestEnvelope[BuildBacktestResult](ctx, c, "/algorithm/index/build", form, referer)
	if err != nil {
		// A session persistence failure after a valid response does not discard
		// the ID of the task we know was created.
		if result.BacktestID != "" {
			return &result, err
		}
		return nil, err
	}
	if result.BacktestID == "" {
		return nil, &SubmissionError{Operation: "build", Cause: backtestProtocol("build response missing task ID")}
	}
	return &result, nil
}

func validateBuildBacktest(editID string, o BuildBacktestOptions) error {
	if strings.TrimSpace(editID) == "" {
		return fmt.Errorf("edit algorithm ID is required")
	}
	if o.Source != nil && strings.TrimSpace(*o.Source) == "" {
		return fmt.Errorf("source is empty")
	}
	if o.StartTime.IsZero() || o.EndTime.IsZero() || o.EndTime.Before(o.StartTime) {
		return fmt.Errorf("valid backtest start/end times are required")
	}
	if o.InitialCapital <= 0 || math.IsNaN(o.InitialCapital) || math.IsInf(o.InitialCapital, 0) {
		return fmt.Errorf("initial capital must be finite and positive")
	}
	if o.Mode != FullBacktest && o.Mode != CompileBacktest {
		return fmt.Errorf("invalid backtest mode")
	}
	switch o.Frequency {
	case BacktestDaily, BacktestMinute, BacktestTick:
	default:
		return fmt.Errorf("backtest frequency must be day, minute or tick")
	}
	return nil
}

func buildBacktestForm(doc *html.Node, o BuildBacktestOptions) (url.Values, error) {
	algorithm := elementByID(doc, "algorithmId")
	code := elementByID(doc, "code")
	if algorithm == nil || code == nil {
		return nil, backtestProtocol("editor form missing")
	}
	formNode := algorithm.Parent
	for formNode != nil && formNode.Data != "form" {
		formNode = formNode.Parent
	}
	if formNode == nil {
		return nil, backtestProtocol("editor form missing")
	}
	form := make(url.Values)
	allowed := map[string]bool{"algorithm[algorithmId]": true, "algorithm[userId]": true,
		"algorithm[accessControl]": true, "algorithm[name]": true, "fontpref": true, "themepref": true}
	for _, input := range findElements(formNode, func(n *html.Node) bool { return n.Data == "input" }) {
		if name := nodeAttribute(input, "name"); allowed[name] {
			form.Set(name, nodeAttribute(input, "value"))
		}
	}
	for _, name := range []string{"algorithm[algorithmId]", "algorithm[userId]", "algorithm[name]"} {
		if strings.TrimSpace(form.Get(name)) == "" {
			return nil, backtestProtocol("required editor field missing")
		}
	}
	if form.Get("algorithm[accessControl]") != "0" {
		return nil, fmt.Errorf("building access-controlled strategies is not supported")
	}
	source := rawNodeText(code)
	if o.Source != nil {
		source = *o.Source
	}
	if strings.TrimSpace(source) == "" {
		return nil, backtestProtocol("editor source is empty")
	}
	if len(source) > maxResponseBodySize {
		return nil, fmt.Errorf("source exceeds size limit")
	}
	token, err := backtestPageToken(doc)
	if err != nil {
		return nil, err
	}
	form.Set("token", token)
	if strings.TrimSpace(o.Name) != "" {
		form.Set("algorithm[name]", strings.TrimSpace(o.Name))
	}
	form.Set("algorithm[code]", base64.StdEncoding.EncodeToString([]byte(source)))
	form.Set("encrType", "base64")
	form.Set("backtest[type]", strconv.Itoa(int(o.Mode)))
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	form.Set("backtest[startTime]", o.StartTime.In(zone).Format("2006-01-02 15:04:05"))
	form.Set("backtest[endTime]", o.EndTime.In(zone).Format("2006-01-02 15:04:05"))
	form.Set("backtest[baseCapital]", strconv.FormatFloat(o.InitialCapital, 'f', -1, 64))
	form.Set("backtest[frequency]", string(o.Frequency))
	form.Set("backtest[pyVersion]", "3")
	form.Set("ajax", "1")
	if o.AllowCredit {
		form.Set("useCredit", "1")
	}
	return form, nil
}

// CancelBacktest resolves a fresh RunID and token from a detail page and sends
// one cancellation request. Pass a detail ID, including a build's BacktestID.
func (c *Client) CancelBacktest(ctx context.Context, detailID string) error {
	q, err := backtestQuery(detailID)
	if err != nil {
		return err
	}
	doc, err := c.backtestHTML(ctx, "/algorithm/backtest/detail", q)
	if err != nil {
		return err
	}
	runID := valueByID(doc, "backtestId")
	if runID == "" {
		return backtestProtocol("cancel run ID missing")
	}
	token, err := backtestPageToken(doc)
	if err != nil {
		return err
	}
	_, err = postBacktestEnvelope[json.RawMessage](ctx, c, "/algorithm/index/cancel",
		url.Values{"backtestId": {runID}, "token": {token}, "ajax": {"1"}},
		c.backtestPageURL("/algorithm/backtest/detail", url.Values{"backtestId": {detailID}}))
	return err
}

var (
	backtestTokenObject = regexp.MustCompile(`(?s)\b(?:window\.)?tokenData\s*=\s*\{([^}]+)\}`)
	backtestTokenName   = regexp.MustCompile(`\bname\s*:\s*("(?:\\.|[^"\\])*")`)
	backtestTokenValue  = regexp.MustCompile(`\bvalue\s*:\s*("(?:\\.|[^"\\])*")`)
)

func backtestPageToken(doc *html.Node) (string, error) {
	for _, script := range findElements(doc, func(n *html.Node) bool { return n.Data == "script" }) {
		match := backtestTokenObject.FindStringSubmatch(rawNodeText(script))
		if len(match) != 2 {
			continue
		}
		name, value := backtestTokenName.FindStringSubmatch(match[1]), backtestTokenValue.FindStringSubmatch(match[1])
		if len(name) != 2 || len(value) != 2 {
			continue
		}
		var key, token string
		if json.Unmarshal([]byte(name[1]), &key) == nil && key == "token" && json.Unmarshal([]byte(value[1]), &token) == nil && token != "" {
			return token, nil
		}
	}
	return "", backtestProtocol("page token missing or unrecognized")
}

func rawNodeText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(n)
	return b.String()
}

// postBacktestEnvelope is intentionally separate from read retries. Disabling
// GetBody and following no redirects also prevents net/http from replaying a
// form on a reused connection or a 307/308 response.
func postBacktestEnvelope[T any](ctx context.Context, c *Client, path string, form url.Values, referer string) (T, error) {
	var zero T
	request, err := c.newRequest(ctx, http.MethodPost, path, nil, strings.NewReader(form.Encode()))
	if err != nil {
		return zero, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", c.baseURL.Scheme+"://"+c.baseURL.Host)
	request.Header.Set("Referer", referer)
	request.GetBody = nil
	return writeBacktestEnvelope[T](ctx, c, request)
}

func writeBacktestEnvelope[T any](ctx context.Context, c *Client, request *http.Request) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	unknown := func(cause error) error {
		return &SubmissionError{Operation: strings.TrimPrefix(request.URL.Path, "/algorithm/"), Cause: cause}
	}
	response, err := client.Do(request)
	if err != nil {
		return zero, unknown(err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if location, e := response.Location(); e == nil && location.Host == c.baseURL.Host && strings.HasPrefix(location.Path, "/user/login") {
			return zero, ErrSessionExpired
		}
		return zero, unknown(&APIError{HTTPStatus: response.StatusCode, Message: "write redirected"})
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize+1))
	if err != nil {
		return zero, unknown(err)
	}
	if len(raw) > maxResponseBodySize {
		return zero, unknown(backtestProtocol("write response exceeds size limit"))
	}
	var wire envelope[json.RawMessage]
	decodeErr := json.Unmarshal(raw, &wire)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiErr := &APIError{HTTPStatus: response.StatusCode, Code: strconv.Itoa(response.StatusCode), Message: response.Status}
		if decodeErr == nil && wire.Code != "" {
			apiErr.Code, apiErr.Status, apiErr.Message = string(wire.Code), string(wire.Status), wire.Message
		}
		if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout {
			return zero, unknown(apiErr)
		}
		if response.StatusCode == http.StatusUnauthorized {
			return zero, ErrSessionExpired
		}
		return zero, apiErr
	}
	if decodeErr != nil {
		return zero, unknown(backtestProtocol("invalid write response"))
	}
	if wire.Code == "" {
		return zero, unknown(backtestProtocol("write response missing code"))
	}
	if err := wire.validate(response.StatusCode); err != nil {
		// A structured rejection is definitive; it still must not trigger a
		// transparent login-and-retry or an automatic useCredit=1 submission.
		if wire.Code == "00000" {
			return zero, unknown(err)
		}
		return zero, err
	}
	var result T
	data := bytes.TrimSpace(wire.Data)
	if len(data) == 0 {
		data = []byte("null")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return zero, unknown(backtestProtocol("invalid write response data"))
	}
	if len(response.Cookies()) > 0 {
		if err := c.saveSession(ctx); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (c *Client) backtestPageURL(path string, query url.Values) string {
	u := *c.baseURL
	u.User = nil
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	u.RawPath, u.Fragment = "", ""
	u.RawQuery = query.Encode()
	return u.String()
}

// Compile-time assertion of the error classification used by callers.
var _ interface {
	error
	Unwrap() error
	Is(error) bool
} = (*SubmissionError)(nil)
