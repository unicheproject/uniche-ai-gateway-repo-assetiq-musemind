package europeana

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// Hit is one result of a search: enough to list the record and to fetch it.
type Hit struct {
	ID        string // record ID, e.g. "/90402/SK_A_2344"
	Title     string
	Type      string // IMAGE, 3D, VIDEO, SOUND, TEXT
	Thumbnail string // empty if the record has none
}

// Search runs a free-text query against the Search API and returns up to
// limit hits, in Europeana's relevance order.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	params := url.Values{"query": {query}, "rows": {strconv.Itoa(limit)}}
	body, err := c.get(ctx, "/record/v2/search.json?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Items   []struct {
			ID         string   `json:"id"`
			Title      []string `json:"title"`
			Type       string   `json:"type"`
			EdmPreview []string `json:"edmPreview"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing europeana search: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("europeana search failed: %s", resp.Error)
	}
	hits := make([]Hit, 0, len(resp.Items))
	for _, it := range resp.Items {
		h := Hit{ID: it.ID, Type: it.Type}
		if len(it.Title) > 0 {
			h.Title = it.Title[0]
		}
		if len(it.EdmPreview) > 0 {
			h.Thumbnail = it.EdmPreview[0]
		}
		hits = append(hits, h)
	}
	return hits, nil
}
