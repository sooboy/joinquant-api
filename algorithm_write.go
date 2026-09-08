package joinquant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// CreateAlgorithm creates one empty, private cloud strategy and returns its
// editor ID. This is a write, even though the website also exposes a GET link.
// The POST variant is used by the website's template generator. Never retry an
// uncertain result automatically: a strategy may already have been created.
func (c *Client) CreateAlgorithm(ctx context.Context) (string, error) {
	return c.createAlgorithm(ctx, "")
}

// CreateAlgorithmInFolder creates directly under a folder returned by
// ListAlgorithms. A missing folder must never silently fall back to the root.
func (c *Client) CreateAlgorithmInFolder(ctx context.Context, folderID string) (string, error) {
	if strings.TrimSpace(folderID) == "" || folderID == "0" {
		return "", fmt.Errorf("non-root folder ID is required")
	}
	return c.createAlgorithm(ctx, folderID)
}

func (c *Client) createAlgorithm(ctx context.Context, folderID string) (string, error) {
	query := url.Values{"restore": {"0"}, "type": {"empty"}, "baseCapital": {"100000"}}
	form := url.Values{"restore": {"0"}, "type": {"empty"}}
	referer := url.Values{}
	if folderID != "" {
		query.Set("fId", folderID)
		form.Set("fId", folderID)
		referer.Set("fId", folderID)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/algorithm/index/new",
		query, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Del("ajax")
	req.URL.RawQuery = q.Encode()
	req.Header.Del("X-Requested-With")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", c.baseURL.Scheme+"://"+c.baseURL.Host)
	req.Header.Set("Referer", c.backtestPageURL("/algorithm/index/list", referer))
	req.GetBody = nil
	if err := ctx.Err(); err != nil {
		return "", err
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	unknown := func(cause error) (string, error) {
		return "", &SubmissionError{Operation: "create algorithm", Cause: cause}
	}
	resp, err := client.Do(req)
	if err != nil {
		return unknown(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location, e := resp.Location()
		if e != nil {
			return unknown(backtestProtocol("create redirect missing"))
		}
		if location.Scheme != c.baseURL.Scheme || location.Host != c.baseURL.Host {
			return unknown(backtestProtocol("unexpected create redirect origin"))
		}
		if strings.HasPrefix(location.Path, "/user/login") {
			return "", ErrSessionExpired
		}
		id := location.Query().Get("algorithmId")
		if location.Path != "/algorithm/index/edit" || id == "" {
			return unknown(backtestProtocol("create redirect missing editor ID"))
		}
		return id, c.saveSession(ctx)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize+1))
	if e != nil {
		return unknown(e)
	}
	var wire envelope[json.RawMessage]
	if json.Unmarshal(raw, &wire) == nil && wire.Code != "" && wire.Code != "00000" && resp.StatusCode < 500 {
		return "", wire.validate(resp.StatusCode)
	}
	return unknown(backtestProtocol("create did not return an editor redirect"))
}
