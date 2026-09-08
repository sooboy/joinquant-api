package joinquant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type LiveHistoryEntry struct {
	LiveHistoryID    string          `json:"liveHistoryId"`
	BacktestID       string          `json:"backtestId"`
	AddedAt          string          `json:"addTime"`
	ModifiedAt       string          `json:"modTime"`
	Code             json.RawMessage `json:"code"`
	SourceBacktestID string          `json:"sourceBacktestId"`
	Status           StringValue     `json:"status"`
	Introduction     *string         `json:"introduce"`
	ShowType         int             `json:"showType"`
	GetCodeType      int             `json:"getCodeType"`
}

type LiveHistoryPage struct {
	Entries    []LiveHistoryEntry `json:"list"`
	TotalCount StringValue        `json:"totalCount"`
}

func (c *Client) LiveHistory(ctx context.Context, runID string, page, limit int) (*LiveHistoryPage, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	query := url.Values{
		"backtestId": {runID},
		"page":       {strconv.Itoa(page)},
		"limit":      {strconv.Itoa(limit)},
	}
	result, err := getEnvelope[LiveHistoryPage](ctx, c, "/algorithm/live/getLiveHistoryList", query)
	if err != nil {
		return nil, err
	}
	if result.Entries == nil {
		result.Entries = make([]LiveHistoryEntry, 0)
	}
	return &result, nil
}
