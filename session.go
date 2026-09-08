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

type SessionInfo struct {
	LoggedIn bool   `json:"-"`
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	Alias    string `json:"alias"`
	VIPType  string `json:"vipType"`
	Level    int    `json:"level"`
}

type sessionData struct {
	IsLogin int `json:"isLogin"`
	SessionInfo
}

// Login authenticates with JoinQuant's password-login endpoint and stores the
// returned cookies only in the client's cookie jar. Credentials are never
// retained by Client.
func (c *Client) Login(ctx context.Context, username, password string) (*SessionInfo, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.loginLocked(ctx, username, password)
}

func (c *Client) loginOnce(ctx context.Context, username, password string) (*SessionInfo, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password are required")
	}
	form := url.Values{
		"username": {username},
		"pwd":      {password},
	}
	encodedForm := form.Encode()
	request, err := c.newRequest(ctx, http.MethodPost, "/user/login/doLoginByText", nil, strings.NewReader(encodedForm))
	if err != nil {
		return nil, err
	}
	loginQuery := request.URL.Query()
	loginQuery.Del("ajax")
	request.URL.RawQuery = loginQuery.Encode()
	request.ContentLength = int64(len(encodedForm))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := c.do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var result envelope[jsonRawData]
	if err := decodeJSON(response.Body, &result); err != nil {
		return nil, fmt.Errorf("%w: decode login response: %v", ErrInvalidResponse, err)
	}
	if err := result.validate(response.StatusCode); err != nil {
		return nil, err
	}
	return c.sessionOnce(ctx)
}

// Session verifies the current cookie jar without exposing account-private
// profile fields beyond the small identity summary returned here.
func (c *Client) Session(ctx context.Context) (*SessionInfo, error) {
	return c.sessionOnce(ctx)
}

func (c *Client) sessionOnce(ctx context.Context) (*SessionInfo, error) {
	data, err := getEnvelopeOnce[sessionData](ctx, c, "/user/index/isLogin", nil)
	if err != nil {
		return nil, err
	}
	if data.IsLogin == 0 {
		return nil, ErrSessionExpired
	}
	data.SessionInfo.LoggedIn = true
	return &data.SessionInfo, nil
}

type jsonRawData map[string]any

func decodeJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxResponseBodySize))
	decoder.UseNumber()
	return decoder.Decode(target)
}
