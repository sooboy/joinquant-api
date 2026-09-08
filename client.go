package joinquant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL      = "https://www.joinquant.com"
	defaultTimeout      = 20 * time.Second
	defaultUserAgent    = "joinquant-api-go/0.1"
	maxResponseBodySize = 32 << 20
)

// Client is a concurrency-safe client for the web endpoints used by JoinQuant's
// simulated-trading and backtest pages. Reads may renew a session once; writes
// are not automatically replayed.
//
// These endpoints are not the official JQData API. They may change when the
// JoinQuant web application changes, so callers should surface API errors and
// avoid silently falling back to unsafe order quantities.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	userAgent  string
	jar        *statefulJar

	sessionStore       SessionStore
	credentialProvider CredentialProvider
	accountKey         string
	authMu             sync.Mutex
	authGeneration     uint64
	sessionLoaded      bool
}

type clientConfig struct {
	baseURL            string
	httpClient         *http.Client
	userAgent          string
	cookies            []*http.Cookie
	sessionStore       SessionStore
	credentialProvider CredentialProvider
	accountKey         string
}

// Option configures a Client.
type Option func(*clientConfig) error

// WithBaseURL overrides the JoinQuant origin. It is primarily useful for tests.
func WithBaseURL(rawURL string) Option {
	return func(cfg *clientConfig) error {
		if strings.TrimSpace(rawURL) == "" {
			return fmt.Errorf("base URL is empty")
		}
		cfg.baseURL = rawURL
		return nil
	}
}

// WithHTTPClient supplies an HTTP client. Its Transport is reused, while the
// value itself is copied so New can safely install a cookie jar when needed.
func WithHTTPClient(client *http.Client) Option {
	return func(cfg *clientConfig) error {
		if client == nil {
			return fmt.Errorf("HTTP client is nil")
		}
		copyOfClient := *client
		cfg.httpClient = &copyOfClient
		return nil
	}
}

func WithUserAgent(userAgent string) Option {
	return func(cfg *clientConfig) error {
		cfg.userAgent = strings.TrimSpace(userAgent)
		return nil
	}
}

// WithCookies seeds the client's cookie jar. Prefer Login when username and
// password authentication is available.
func WithCookies(cookies ...*http.Cookie) Option {
	return func(cfg *clientConfig) error {
		cfg.cookies = append(cfg.cookies, cookies...)
		return nil
	}
}

// WithSessionStore enables durable session cookies for one JoinQuant account.
// The store contains bearer credentials and must protect them appropriately.
func WithSessionStore(accountKey string, store SessionStore) Option {
	return func(cfg *clientConfig) error {
		if strings.TrimSpace(accountKey) == "" {
			return fmt.Errorf("session account key is empty")
		}
		if store == nil {
			return fmt.Errorf("session store is nil")
		}
		cfg.accountKey = strings.TrimSpace(accountKey)
		cfg.sessionStore = store
		return nil
	}
}

// WithCredentialProvider enables automatic login when a restored session has
// expired. Client requests credentials only when a login is actually required.
func WithCredentialProvider(provider CredentialProvider) Option {
	return func(cfg *clientConfig) error {
		if provider == nil {
			return fmt.Errorf("credential provider is nil")
		}
		cfg.credentialProvider = provider
		return nil
	}
}

func New(options ...Option) (*Client, error) {
	cfg := clientConfig{
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: defaultTimeout},
		userAgent:  defaultUserAgent,
	}
	for _, option := range options {
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}

	baseURL, err := url.Parse(cfg.baseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q", cfg.baseURL)
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")

	var baseJar http.CookieJar = cfg.httpClient.Jar
	if baseJar == nil {
		baseJar, err = cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("create cookie jar: %w", err)
		}
	}
	jar := newStatefulJar(baseJar)
	cfg.httpClient.Jar = jar
	if existing := baseJar.Cookies(baseURL); len(existing) > 0 {
		jar.SetCookies(baseURL, existing)
	}
	if len(cfg.cookies) > 0 {
		jar.SetCookies(baseURL, cfg.cookies)
	}

	return &Client{
		baseURL: baseURL, httpClient: cfg.httpClient, userAgent: cfg.userAgent, jar: jar,
		sessionStore: cfg.sessionStore, credentialProvider: cfg.credentialProvider, accountKey: cfg.accountKey,
	}, nil
}

// Cookies returns a copy of the cookies currently stored for JoinQuant.
// Callers must treat their values as secrets.
func (c *Client) Cookies() []*http.Cookie {
	cookies := c.httpClient.Jar.Cookies(c.baseURL)
	result := make([]*http.Cookie, len(cookies))
	for i, cookie := range cookies {
		copyOfCookie := *cookie
		result[i] = &copyOfCookie
	}
	return result
}

// StringValue accepts either a JSON string or number. JoinQuant uses both
// representations for fields such as status, code, offset and totalCount.
type StringValue string

func (s *StringValue) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = StringValue(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		*s = StringValue(number.String())
		return nil
	}
	return fmt.Errorf("expected string or number, got %s", string(data))
}

type envelope[T any] struct {
	Data     T           `json:"data"`
	Status   StringValue `json:"status"`
	Code     StringValue `json:"code"`
	Message  string      `json:"msg"`
	Redirect string      `json:"redirect"`
}

func (e envelope[T]) validate(httpStatus int) error {
	code := string(e.Code)
	status := string(e.Status)
	if e.Redirect != "" && strings.Contains(e.Redirect, "/user/login") {
		return ErrSessionExpired
	}
	if code == "10001" || code == "401" {
		return ErrSessionExpired
	}
	if code == "00000" && (status == "" || status == "0" || status == "success") {
		return nil
	}
	return &APIError{HTTPStatus: httpStatus, Status: status, Code: code, Message: e.Message}
}

func (c *Client) endpoint(path string, query url.Values) string {
	result := *c.baseURL
	result.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	if query == nil {
		query = make(url.Values)
	}
	if query.Get("ajax") == "" {
		query.Set("ajax", "1")
	}
	result.RawQuery = query.Encode()
	return result.String()
}

func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, query), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/html;q=0.9")
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	return request, nil
}

func (c *Client) do(request *http.Request) (*http.Response, error) {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.Request != nil && response.Request.URL != nil && response.Request.URL.Path == "/user/login/index" {
		response.Body.Close()
		return nil, ErrSessionExpired
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, &APIError{HTTPStatus: response.StatusCode, Code: strconv.Itoa(response.StatusCode), Message: response.Status}
	}
	if len(response.Cookies()) > 0 {
		if err := c.saveSession(request.Context()); err != nil {
			response.Body.Close()
			return nil, err
		}
	}
	return response, nil
}

func getEnvelope[T any](ctx context.Context, c *Client, path string, query url.Values) (T, error) {
	return getEnvelopeHeaders[T](ctx, c, path, query, nil)
}

func getEnvelopeHeaders[T any](ctx context.Context, c *Client, path string, query url.Values, headers http.Header) (T, error) {
	generation := c.currentAuthGeneration()
	result, err := getEnvelopeOnceHeaders[T](ctx, c, path, query, headers)
	if err == nil || !errors.Is(err, ErrSessionExpired) || c.credentialProvider == nil {
		return result, err
	}
	if err := c.reauthenticate(ctx, generation); err != nil {
		var zero T
		return zero, err
	}
	return getEnvelopeOnceHeaders[T](ctx, c, path, query, headers)
}

func getEnvelopeOnce[T any](ctx context.Context, c *Client, path string, query url.Values) (T, error) {
	return getEnvelopeOnceHeaders[T](ctx, c, path, query, nil)
}

func getEnvelopeOnceHeaders[T any](ctx context.Context, c *Client, path string, query url.Values, headers http.Header) (T, error) {
	var zero T
	request, err := c.newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return zero, err
	}
	for key, values := range headers {
		request.Header[key] = append([]string(nil), values...)
	}
	response, err := c.do(request)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()

	var result envelope[T]
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBodySize))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return zero, fmt.Errorf("%w: decode %s: %v", ErrInvalidResponse, path, err)
	}
	if err := result.validate(response.StatusCode); err != nil {
		return zero, err
	}
	return result.Data, nil
}

func (c *Client) getHTML(ctx context.Context, path string, query url.Values) (io.ReadCloser, error) {
	generation := c.currentAuthGeneration()
	body, err := c.getHTMLOnce(ctx, path, query)
	if err == nil || !errors.Is(err, ErrSessionExpired) || c.credentialProvider == nil {
		return body, err
	}
	if err := c.reauthenticate(ctx, generation); err != nil {
		return nil, err
	}
	return c.getHTMLOnce(ctx, path, query)
}

func (c *Client) getHTMLOnce(ctx context.Context, path string, query url.Values) (io.ReadCloser, error) {
	request, err := c.newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	htmlQuery := request.URL.Query()
	htmlQuery.Del("ajax")
	request.URL.RawQuery = htmlQuery.Encode()
	request.Header.Set("Accept", "text/html")
	// The simulated-trading list is a full-page navigation endpoint. Sending
	// XMLHttpRequest makes JoinQuant return a partial page without #live_table.
	request.Header.Del("X-Requested-With")
	response, err := c.do(request)
	if err != nil {
		return nil, err
	}
	contentType := response.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "text/html") {
		response.Body.Close()
		return nil, fmt.Errorf("%w: expected HTML, got %s", ErrInvalidResponse, contentType)
	}
	return response.Body, nil
}
