package joinquant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// CreateAlgorithmFolder is a single non-replayed write. Root parent is "0".
// On an unknown outcome, list folders and reconcile by name before proceeding.
func (c *Client) CreateAlgorithmFolder(ctx context.Context, name, parentID string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("folder name is required")
	}
	if parentID == "" {
		parentID = "0"
	}
	req, err := c.folderWriteRequest(ctx, "/algorithm/index/AddFile", url.Values{"pId": {parentID}}, url.Values{"name": {name}}, parentID)
	if err != nil {
		return "", err
	}
	id, err := writeBacktestEnvelope[StringValue](ctx, c, req)
	if err != nil {
		return string(id), err
	}
	if strings.TrimSpace(string(id)) == "" || id == "0" {
		return "", &SubmissionError{Operation: "create folder", Cause: backtestProtocol("folder response missing ID")}
	}
	return string(id), nil
}

// MoveAlgorithms expects fresh Algorithm.OperationID values, not editor or
// history IDs. Confirm destination membership after an uncertain response.
func (c *Client) MoveAlgorithms(ctx context.Context, operationIDs []string, folderID string) error {
	if len(operationIDs) == 0 || strings.TrimSpace(folderID) == "" || folderID == "0" {
		return fmt.Errorf("strategy IDs and a non-root destination folder are required")
	}
	for _, id := range operationIDs {
		if strings.TrimSpace(id) == "" || strings.Contains(id, ",") {
			return fmt.Errorf("invalid strategy operation ID")
		}
	}
	req, err := c.folderWriteRequest(ctx, "/algorithm/index/AlgorithmToFile", url.Values{"ids": {strings.Join(operationIDs, ",")}, "fId": {folderID}}, nil, "0")
	if err != nil {
		return err
	}
	_, err = writeBacktestEnvelope[json.RawMessage](ctx, c, req)
	return err
}

func (c *Client) folderWriteRequest(ctx context.Context, path string, query, form url.Values, parentID string) (*http.Request, error) {
	if form == nil {
		form = url.Values{}
	}
	form.Set("ajax", "1")
	req, err := c.newRequest(ctx, http.MethodPost, path, query, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", c.baseURL.Scheme+"://"+c.baseURL.Host)
	req.Header.Set("Referer", c.backtestPageURL("/algorithm/index/list", url.Values{"fId": {parentID}}))
	req.GetBody = nil
	return req, nil
}
