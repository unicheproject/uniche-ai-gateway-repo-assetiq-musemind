// Package europeana is a client for the Europeana Record API. It returns
// Europeana-shaped data and knows nothing about AssetIQ or the Repository.
package europeana

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultBaseURL = "https://api.europeana.eu"
	maxRecordBytes = 10 << 20
)

// ErrNotFound means Europeana has no record with the requested ID.
var ErrNotFound = errors.New("europeana record not found")

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at another Record API host, e.g. a test server.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL: defaultBaseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Record is one Europeana record in both representations: the parsed JSON
// form for field mapping, and the JSON-LD form kept verbatim.
type Record struct {
	Object Object
	JSONLD []byte
}

// Record fetches the record with the given ID, e.g. "/90402/SK_A_2344".
func (c *Client) Record(ctx context.Context, id string) (*Record, error) {
	body, err := c.get(ctx, "/record/v2"+id+".json")
	if err != nil {
		return nil, err
	}
	obj, err := parseObject(body)
	if err != nil {
		return nil, fmt.Errorf("parsing europeana record %s: %w", id, err)
	}

	jsonld, err := c.get(ctx, "/record/v2"+id+".jsonld")
	if err != nil {
		return nil, err
	}
	return &Record{Object: *obj, JSONLD: jsonld}, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("building europeana request %s: %w", path, err)
	}
	// The key goes in a header, not the query string, so it never lands in
	// logged URLs.
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling europeana %s: %w", path, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("calling europeana %s: unexpected status %s", path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRecordBytes))
	if err != nil {
		return nil, fmt.Errorf("reading europeana %s: %w", path, err)
	}
	return body, nil
}
