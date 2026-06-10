package plex

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/httpx"
	"github.com/cplieger/tautulli-remap/internal/remap"
)

// Response body size limits.
const (
	maxPlexBody     = 40 << 20
	maxPlexSections = 10 << 20
)

// Section is an alias for remap.Section (kept for backward compatibility within this package).
type Section = remap.Section

// LibItem is an alias for remap.LibItem (kept for backward compatibility within this package).
type LibItem = remap.LibItem

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

func (c *Client) ItemExists(ctx context.Context, ratingKey string) bool {
	if !remap.RatingKey(ratingKey).IsValid() {
		slog.Warn("invalid rating key (non-numeric)", "key", ratingKey)
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/library/metadata/"+ratingKey, http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Debug("plex check failed", "key", ratingKey, "error", err)
		return false
	}
	defer resp.Body.Close()
	httpx.DrainClose(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		slog.Warn("plex check unexpected status", "key", ratingKey, "status", resp.StatusCode)
	}
	return resp.StatusCode == http.StatusOK
}

func (c *Client) LibrarySections(ctx context.Context) []Section {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/library/sections", http.NoBody)
	if err != nil {
		slog.Error("failed to create Plex sections request", "error", err)
		return nil
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("failed to get Plex sections", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpx.DrainClose(resp.Body)
		slog.Error("Plex sections returned non-200", "status", resp.StatusCode)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlexSections))
	if err != nil {
		slog.Error("failed to read Plex sections response", "error", err)
		return nil
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
		slog.Error("failed to parse Plex sections", "error", err)
		return nil
	}

	var sections []Section
	for _, d := range result.MediaContainer.Directory {
		sections = append(sections, Section{Key: d.Key, Title: d.Title, Type: d.Type})
	}
	return sections
}

func (c *Client) LibraryAll(ctx context.Context, sectionKey string) []LibItem {
	if !remap.RatingKey(sectionKey).IsValid() {
		slog.Warn("invalid section key (non-numeric)", "key", sectionKey)
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/library/sections/"+sectionKey+"/all", http.NoBody)
	if err != nil {
		slog.Error("failed to create Plex library request", "error", err)
		return nil
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("failed to get Plex library items", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpx.DrainClose(resp.Body)
		slog.Error("Plex library returned non-200", "status", resp.StatusCode, "section", sectionKey)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlexBody))
	if err != nil {
		slog.Error("failed to read Plex library response", "error", err)
		return nil
	}

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
		slog.Error("failed to parse Plex library", "error", err)
		return nil
	}

	var items []LibItem
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
		items = append(items, LibItem{
			RatingKey: rk, Title: m.Title, Year: m.Year, GUIDs: guids,
		})
	}
	return items
}
