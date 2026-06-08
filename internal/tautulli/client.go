package tautulli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"time"

	"github.com/cplieger/httpx"
	"github.com/cplieger/tautulli-remap/internal/httputil"
	"github.com/cplieger/tautulli-remap/internal/remap"
)

// Response body size limit.
const maxTautulliBody = 30 << 20

// resultSuccess is the expected value of Response.Result on success.
const resultSuccess = "success"

// defaultRetryDelayUnit is the base delay multiplied by attempt number.
const defaultRetryDelayUnit = 5 * time.Second

// Client is the concrete Tautulli API client.
type Client struct {
	httpClient *http.Client
	logger     *slog.Logger
	url        string
	apiKey     string

	// RetryDelayUnit is the base delay multiplied by attempt number. Zero uses the default.
	RetryDelayUnit time.Duration
}

// HistoryResponse represents the JSON envelope for get_history.
type HistoryResponse struct {
	Response struct {
		Result  string `json:"result"`
		Message string `json:"message"`
		Data    struct {
			Data            []remap.HistoryItem `json:"data"`
			RecordsFiltered int                 `json:"recordsFiltered"`
		} `json:"data"`
	} `json:"response"`
}

// Result represents a generic Tautulli API result envelope.
type Result struct {
	Response struct {
		Result  string `json:"result"`
		Message string `json:"message"`
	} `json:"response"`
}

// HistoryPage holds a parsed page of history data.
type HistoryPage struct {
	Rows            []remap.HistoryItem
	RecordsFiltered int
}

// New creates a new Tautulli client.
func New(tautulliURL, apiKey string, httpClient *http.Client) *Client {
	return &Client{
		httpClient: httpClient,
		logger:     redactedLogger(apiKey),
		url:        tautulliURL,
		apiKey:     apiKey,
	}
}

// requestURL builds the Tautulli API v2 URL for cmd with the api key and any
// extra query params. The api key rides in the query string, so error messages
// derived from this URL must be redacted (see API / APIWithRetry).
func (c *Client) requestURL(cmd string, extra url.Values) string {
	params := url.Values{"cmd": {cmd}, "apikey": {c.apiKey}}
	maps.Copy(params, extra)
	return c.url + "/api/v2?" + params.Encode()
}

func (c *Client) API(ctx context.Context, cmd string, extra url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.requestURL(cmd, extra), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("tautulli %s: %w", cmd, httputil.SanitizeErr(err))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tautulli %s: %w", cmd, httputil.SanitizeErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httputil.DrainBody(resp.Body)
		return nil, fmt.Errorf("tautulli %s: HTTP %d", cmd, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxTautulliBody))
}

// APIWithRetry performs a GET against the Tautulli API with bounded
// exponential-backoff retry on 429/5xx (honoring Retry-After) via
// github.com/cplieger/httpx. 4xx-other-than-429 and non-transient transport
// errors are returned immediately. The api key (carried in the query string)
// is redacted from any returned error. Used for read commands; mutating
// commands call API directly so they are never retried.
func (c *Client) APIWithRetry(ctx context.Context, cmd string, extra url.Values) ([]byte, error) {
	body, err := httpx.Retry(ctx, c.httpClient, c.requestURL(cmd, extra),
		httpx.WithMaxAttempts(3),
		httpx.WithBaseDelay(c.retryDelayUnit()),
		httpx.WithMaxBodyBytes(maxTautulliBody),
		httpx.WithLogger(c.logger),
	)
	if err != nil {
		return nil, fmt.Errorf("tautulli %s: %w", cmd, httpx.RedactSecret(err, c.apiKey))
	}
	return body, nil
}

func (c *Client) GetHistory(ctx context.Context, params url.Values) (*HistoryPage, error) {
	body, err := c.APIWithRetry(ctx, "get_history", params)
	if err != nil {
		return nil, err
	}
	var resp HistoryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing history: %w", err)
	}
	if resp.Response.Result != resultSuccess {
		return nil, fmt.Errorf("get_history: %s", resp.Response.Message)
	}
	return &HistoryPage{
		Rows:            resp.Response.Data.Data,
		RecordsFiltered: resp.Response.Data.RecordsFiltered,
	}, nil
}

func (c *Client) UpdateMetadata(ctx context.Context, oldKey, newKey string, mediaType remap.MediaType) error {
	params := url.Values{
		"old_rating_key": {oldKey},
		"new_rating_key": {newKey},
		"media_type":     {string(mediaType)},
	}
	body, err := c.API(ctx, "update_metadata_details", params)
	if err != nil {
		return err
	}
	var resp Result
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parsing update_metadata_details: %w", err)
	}
	if resp.Response.Result != resultSuccess {
		return fmt.Errorf("update_metadata_details: %s", resp.Response.Message)
	}
	return nil
}

func (c *Client) DeleteRecentlyAdded(ctx context.Context) error {
	body, err := c.API(ctx, "delete_recently_added", nil)
	if err != nil {
		return err
	}
	var resp Result
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parsing delete_recently_added: %w", err)
	}
	if resp.Response.Result != resultSuccess {
		return fmt.Errorf("delete_recently_added: %s", resp.Response.Message)
	}
	return nil
}

// retryDelayUnit returns the configured retry delay unit or the default.
func (c *Client) retryDelayUnit() time.Duration {
	if c.RetryDelayUnit > 0 {
		return c.RetryDelayUnit
	}
	return defaultRetryDelayUnit
}
