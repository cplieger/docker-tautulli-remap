package tautulli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"time"

	"tautulli-remap/internal/httputil"
	"tautulli-remap/internal/remap"
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
	return &Client{httpClient: httpClient, url: tautulliURL, apiKey: apiKey}
}

func (c *Client) API(ctx context.Context, cmd string, extra url.Values) ([]byte, error) {
	params := url.Values{"cmd": {cmd}, "apikey": {c.apiKey}}
	maps.Copy(params, extra)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/api/v2?"+params.Encode(), http.NoBody)
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
		err := fmt.Errorf("tautulli %s: HTTP %d", cmd, resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return nil, &remap.NonRetryableError{Err: err}
		}
		return nil, err
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxTautulliBody))
}

func (c *Client) APIWithRetry(ctx context.Context, cmd string, extra url.Values) ([]byte, error) {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			delay := time.Duration(attempt) * c.retryDelayUnit()
			slog.Warn("retrying Tautulli API", "cmd", cmd, "attempt", attempt+1, "delay", delay)
			if err := httputil.SleepCtx(ctx, delay); err != nil {
				return nil, err
			}
		}
		body, err := c.API(ctx, cmd, extra)
		if err == nil {
			return body, nil
		}
		lastErr = err
		var nr *remap.NonRetryableError
		if errors.As(err, &nr) {
			return nil, err
		}
	}
	return nil, lastErr
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
