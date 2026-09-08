package joinquant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type BacktestExportKind string

const (
	ExportSummary      BacktestExportKind = "summary"
	ExportTransactions BacktestExportKind = "transaction"
	ExportPositions    BacktestExportKind = "position"
	ExportLogs         BacktestExportKind = "log"
)

func (k BacktestExportKind) Valid() bool {
	return k == ExportSummary || k == ExportTransactions || k == ExportPositions || k == ExportLogs
}

var ErrExportCreditsRequired = errors.New("聚宽要求扣除积分，未申请付费导出；请等待免费额度恢复或检查账号权益")

// Membership reads the signed-in account's current tier. It does not infer a
// global remaining-export count: the website's credit endpoint exposes none.
func (c *Client) Membership(ctx context.Context) (string, error) {
	w, e := getEnvelope[struct {
		VIPType *string `json:"vipType"`
	}](ctx, c, "/community/post/userInfo", nil)
	if e != nil {
		return "", e
	}
	if w.VIPType == nil {
		return "", backtestProtocol("membership missing")
	}
	switch tier := strings.ToUpper(*w.VIPType); tier {
	case "":
		return "FREE", nil
	case "VIP", "SVIP":
		return tier, nil
	default:
		return tier, nil
	}
}

// CreateBacktestExport consumes only an available free entitlement. JoinQuant
// implements this mutation as GET; it must never use normal GET/auth retries.
// IDs on the three export buttons differ from both DetailID and RunID.
func (c *Client) CreateBacktestExport(ctx context.Context, detailID string, kind BacktestExportKind) (string, error) {
	if !kind.Valid() || kind == ExportSummary {
		return "", fmt.Errorf("invalid ZIP export kind")
	}
	q, e := backtestQuery(detailID)
	if e != nil {
		return "", e
	}
	doc, e := c.backtestHTML(ctx, "/algorithm/backtest/detail", q)
	if e != nil {
		return "", e
	}
	n := elementByID(doc, "export-"+string(kind)+"-button")
	if n == nil || nodeAttribute(n, "backtestid") == "" || nodeAttribute(n, "type") != string(kind) {
		return "", backtestProtocol("export button missing")
	}
	q = url.Values{"backtestId": {nodeAttribute(n, "backtestid")}, "type": {string(kind)}, "useCredit": {"0"}}
	req, e := c.newRequest(ctx, http.MethodGet, "/algorithm/backtest/addExportZip", q, nil)
	if e != nil {
		return "", e
	}
	// A non-replayable, unknown-length empty body prevents net/http's implicit
	// replay of GET on a broken reused connection. Redirects are also disabled.
	req.Body = io.NopCloser(strings.NewReader(""))
	req.ContentLength = -1
	req.GetBody = nil
	req.Header.Set("Referer", c.backtestPageURL("/algorithm/backtest/detail", url.Values{"backtestId": {detailID}}))
	task, e := writeBacktestEnvelope[StringValue](ctx, c, req)
	var apiErr *APIError
	if errors.As(e, &apiErr) && apiErr.Code == "30000" {
		return "", errors.Join(ErrExportCreditsRequired, e)
	}
	if task == "" && e == nil {
		return "", &SubmissionError{Operation: "export", Cause: backtestProtocol("export task missing")}
	}
	return string(task), e
}

// BacktestExportStatus returns 0 (packing), 1 (ready), or 2 (no data).
func (c *Client) BacktestExportStatus(ctx context.Context, task string) (int, error) {
	if strings.TrimSpace(task) == "" {
		return 0, fmt.Errorf("export task is required")
	}
	raw, e := getEnvelope[json.RawMessage](ctx, c, "/algorithm/backtest/getExportStatus", url.Values{"task": {task}})
	if e != nil {
		return 0, e
	}
	// The live endpoint returns false while the asynchronous task is packing.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
		return 0, nil
	}
	var s IntValue
	if e = json.Unmarshal(raw, &s); e != nil {
		return 0, backtestProtocol("unrecognized export status")
	}
	if s < 0 || s > 2 {
		return 0, backtestProtocol("unrecognized export status")
	}
	return int(s), nil
}

// DownloadBacktestExport reuses an existing task; it does not spend another
// free export. Full archives can be larger than the normal JSON response cap.
func (c *Client) DownloadBacktestExport(ctx context.Context, task string, dst io.Writer) error {
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("export task is required")
	}
	return c.downloadExport(ctx, "/algorithm/backtest/getExportZip", url.Values{"task": {task}}, dst, true)
}

func (c *Client) DownloadBacktestSummary(ctx context.Context, detailID string, dst io.Writer) error {
	d, e := c.Backtest(ctx, detailID)
	if e != nil {
		return e
	}
	return c.downloadExport(ctx, "/algorithm/backtest/export", url.Values{"backtestId": {d.RunID}, "type": {"result"}}, dst, false)
}

func (c *Client) downloadExport(ctx context.Context, path string, q url.Values, dst io.Writer, zipped bool) error {
	req, e := c.newRequest(ctx, http.MethodGet, path, q, nil)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/octet-stream, text/csv")
	req.Header.Del("X-Requested-With")
	req.Header.Set("Referer", c.backtestPageURL("/algorithm/index/list", nil))
	query := req.URL.Query()
	query.Del("ajax")
	req.URL.RawQuery = query.Encode()
	client := *c.httpClient
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Host != c.baseURL.Host || next.URL.Scheme != c.baseURL.Scheme {
			return http.ErrUseLastResponse
		}
		if len(via) > 5 {
			return fmt.Errorf("too many download redirects")
		}
		return nil
	}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.Request != nil && strings.HasPrefix(resp.Request.URL.Path, "/user/login") {
		return ErrSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{HTTPStatus: resp.StatusCode, Code: fmt.Sprint(resp.StatusCode), Message: "export download failed"}
	}
	b := bufio.NewReader(resp.Body)
	prefix, _ := b.Peek(512)
	text := strings.TrimSpace(strings.TrimPrefix(string(prefix), "\ufeff"))
	if strings.Contains(resp.Header.Get("Content-Type"), "json") || strings.HasPrefix(text, "{") {
		raw, e := io.ReadAll(io.LimitReader(b, maxResponseBodySize))
		if e != nil {
			return e
		}
		var wire envelope[json.RawMessage]
		if json.Unmarshal(raw, &wire) == nil {
			if e = wire.validate(resp.StatusCode); e != nil {
				return e
			}
		}
		return backtestProtocol("download returned JSON instead of a file")
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") || strings.HasPrefix(text, "<") {
		return backtestProtocol("download returned HTML instead of a file")
	}
	if zipped && !(len(prefix) >= 4 && string(prefix[:2]) == "PK" && (string(prefix[2:4]) == "\x03\x04" || string(prefix[2:4]) == "\x05\x06")) {
		return backtestProtocol("download is not a ZIP archive")
	}
	const maxExportBytes = 128 << 20
	n, e := io.Copy(dst, io.LimitReader(b, maxExportBytes+1))
	if e != nil {
		return e
	}
	if n == 0 || n > maxExportBytes {
		return backtestProtocol("export file empty or exceeds 128 MiB")
	}
	return nil
}
