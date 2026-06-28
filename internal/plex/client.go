// Package plex provides a client for the Plex Media Server HTTP API.
package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/httpx/v2"
	"github.com/cplieger/tautulli-remap/internal/remap"
)

// Response body size limits.
const (
	maxPlexBody     = 40 << 20
	maxPlexSections = 10 << 20
)

// plexReadBaseDelay is the base backoff between retry attempts for Plex read
// requests (the existence check and the library index fetches).
const plexReadBaseDelay = 200 * time.Millisecond

// plexReadMaxAttempts is the total number of attempts (including the first) for
// a Plex read request before a transient failure is surfaced to the caller.
const plexReadMaxAttempts = 3

// Client is the concrete Plex API client.
type Client struct {
	httpClient *http.Client
	url        string
	token      string
}

// New creates a new Plex client.
func New(plexURL, token string, httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient, url: plexURL, token: token}
}

// newPlexRequest builds an authenticated GET request for a Plex read endpoint,
// setting the X-Plex-Token and Accept headers every Plex read shares. The
// caller owns ctx (and thus the per-attempt timeout).
func (c *Client) newPlexRequest(ctx context.Context, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// ItemExists reports whether a Plex library item with the given rating key
// currently exists on the server. A non-numeric key is treated as not-exists
// (false, nil) rather than a Plex error, since it can never be a real key. The
// check is retried with bounded exponential backoff on transient failures
// (timeouts, connection resets, DNS errors, and 502/503/504); a 200 means the
// item exists (true, nil) and a 404 means it definitively does not
// (false, nil). A non-nil error means existence could NOT be determined (auth
// failure, rate limit, persistent 5xx, or transport error): callers must
// fail closed and NOT treat the item as stale on error.
func (c *Client) ItemExists(ctx context.Context, ratingKey string) (bool, error) {
	if !remap.RatingKey(ratingKey).IsValid() {
		slog.Warn("invalid rating key (non-numeric)", "key", ratingKey)
		return false, nil
	}
	return httpx.RetryWithBackoff[bool](ctx, plexReadMaxAttempts, plexReadBaseDelay, "plex item check",
		func(ctx context.Context) (bool, error) {
			return c.itemExistsOnce(ctx, ratingKey)
		})
}

// itemExistsOnce performs a single Plex metadata existence check with a
// per-attempt 30s timeout. It returns (true, nil) on 200 and (false, nil) on
// 404; a 5xx maps to a transient *httpx.HTTPStatusError (502/503/504 retried),
// while 401/403 (*AuthError) and 429 (*RateLimitError) are non-transient and
// surface immediately. Transport errors are returned for httpx to classify.
func (c *Client) itemExistsOnce(ctx context.Context, ratingKey string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := c.newPlexRequest(ctx, "/library/metadata/"+ratingKey)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	httpx.DrainClose(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, httpx.CheckHTTPStatus(resp)
	}
}

// LibrarySections returns the list of all library sections from the Plex
// server. Only movie and show sections are used by the remap workflow. A
// non-nil error (request-build, transport, non-200, read, or parse failure)
// means the section list could not be fetched; callers must treat that as a
// degraded Plex rather than an empty library.
func (c *Client) LibrarySections(ctx context.Context) ([]remap.Section, error) {
	return httpx.RetryWithBackoff[[]remap.Section](ctx, plexReadMaxAttempts, plexReadBaseDelay, "plex sections",
		func(ctx context.Context) ([]remap.Section, error) {
			return c.librarySectionsOnce(ctx)
		})
}

// librarySectionsOnce performs a single fetch of the Plex library section list
// with a per-attempt 30s timeout. A 5xx maps to an *HTTPStatusError, of which
// only 502/503/504 are transient (retried by LibrarySections); other 5xx are
// non-transient and surface immediately, as do 401/403 (*AuthError) and 429
// (*RateLimitError). A parse failure is non-transient and surfaces as-is, but a
// transient read failure (a truncated body or a connection reset mid-read) is
// retried by LibrarySections.
func (c *Client) librarySectionsOnce(ctx context.Context) ([]remap.Section, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := c.newPlexRequest(ctx, "/library/sections")
	if err != nil {
		return nil, fmt.Errorf("build Plex sections request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Plex sections: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpx.DrainClose(resp.Body)
		if statusErr := httpx.CheckHTTPStatus(resp); statusErr != nil {
			return nil, statusErr
		}
		return nil, fmt.Errorf("unexpected Plex sections status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlexSections))
	if err != nil {
		return nil, fmt.Errorf("read Plex sections response: %w", err)
	}

	var result struct {
		MediaContainer struct {
			Directory []struct {
				Key   string `json:"key"`
				Title string `json:"title"`
				Type  string `json:"type"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse Plex sections: %w", err)
	}

	var sections []remap.Section
	for _, d := range result.MediaContainer.Directory {
		sections = append(sections, remap.Section{Key: d.Key, Title: d.Title, Type: d.Type})
	}
	return sections, nil
}

// LibraryAll returns all items in the Plex library section identified by
// sectionKey. Each item includes its rating key, title, year, and any
// associated GUIDs for cross-source matching. A non-nil error (invalid key,
// request-build, transport, non-200, read, or parse failure) means the section
// could not be fetched; callers must treat that as a degraded Plex rather than
// an empty section, so a partial outage is not mistaken for missing content.
func (c *Client) LibraryAll(ctx context.Context, sectionKey string) ([]remap.LibItem, error) {
	if !remap.RatingKey(sectionKey).IsValid() {
		return nil, fmt.Errorf("invalid section key (non-numeric): %q", sectionKey)
	}
	return httpx.RetryWithBackoff[[]remap.LibItem](ctx, plexReadMaxAttempts, plexReadBaseDelay, "plex library",
		func(ctx context.Context) ([]remap.LibItem, error) {
			return c.libraryAllOnce(ctx, sectionKey)
		})
}

// libraryAllOnce performs a single fetch of one Plex library section's items
// with a per-attempt 60s timeout. A 5xx maps to an *HTTPStatusError, of which
// only 502/503/504 are transient (retried by LibraryAll); other 5xx are
// non-transient and surface immediately, as do 401/403 (*AuthError) and 429
// (*RateLimitError). A parse failure is non-transient and surfaces as-is, but a
// transient read failure (a truncated body or a connection reset mid-read) is
// retried by LibraryAll.
func (c *Client) libraryAllOnce(ctx context.Context, sectionKey string) ([]remap.LibItem, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := c.newPlexRequest(ctx, "/library/sections/"+sectionKey+"/all")
	if err != nil {
		return nil, fmt.Errorf("build Plex library request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Plex library items: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpx.DrainClose(resp.Body)
		if statusErr := httpx.CheckHTTPStatus(resp); statusErr != nil {
			return nil, statusErr
		}
		return nil, fmt.Errorf("unexpected Plex library status %d for section %s", resp.StatusCode, sectionKey)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlexBody))
	if err != nil {
		return nil, fmt.Errorf("read Plex library response: %w", err)
	}
	items, err := parseLibraryItems(body)
	if err != nil {
		return nil, fmt.Errorf("parse Plex library: %w", err)
	}
	return items, nil
}

// parseLibraryItems unmarshals a Plex /library/sections/{key}/all response
// body into LibItems. Entries with a non-numeric rating key are skipped, and
// each item's GUIDs are normalized (unsupported formats dropped).
func parseLibraryItems(body []byte) ([]remap.LibItem, error) {
	var result struct {
		MediaContainer struct {
			Metadata []struct {
				Title     string `json:"title"`
				RatingKey string `json:"ratingKey"`
				GUID      string `json:"guid"`
				Guids     []struct {
					ID string `json:"id"`
				} `json:"Guid"`
				Year int `json:"year"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var items []remap.LibItem
	for _, m := range result.MediaContainer.Metadata {
		rk, err := strconv.Atoi(m.RatingKey)
		if err != nil {
			slog.Warn("invalid rating key", "key", m.RatingKey, "title", m.Title)
			continue
		}
		var guids []string
		if g := remap.NormalizeGUID(m.GUID); g != "" {
			guids = append(guids, g)
		}
		for _, g := range m.Guids {
			if ng := remap.NormalizeGUID(g.ID); ng != "" {
				guids = append(guids, ng)
			}
		}
		items = append(items, remap.LibItem{
			RatingKey: rk, Title: m.Title, Year: m.Year, GUIDs: guids,
		})
	}
	return items, nil
}
