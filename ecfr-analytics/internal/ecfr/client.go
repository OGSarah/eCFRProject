package ecfr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	base string
	hc   *http.Client
}

func NewClient(base string, timeout time.Duration) *Client {
	return &Client{
		base: base,
		hc:  &http.Client{Timeout: timeout},
	}
}

// Titles endpoint is part of the Versioner service (JSON list of titles). :contentReference[oaicite:3]{index=3}
func (c *Client) GetTitles(ctx context.Context) ([]Title, error) {
	u := c.base + "/api/versioner/v1/titles.json"
	var resp struct {
		Titles []Title `json:"titles"`
	}
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	return resp.Titles, nil
}

// Agencies admin feed. :contentReference[oaicite:4]{index=4}
func (c *Client) GetAgencies(ctx context.Context) ([]Agency, error) {
	u := c.base + "/api/admin/v1/agencies.json"
	var resp struct {
		Agencies []Agency `json:"agencies"`
	}
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	return resp.Agencies, nil
}

// Full-title XML. :contentReference[oaicite:5]{index=5}
func (c *Client) GetFullTitleXML(ctx context.Context, date string, title int) ([]byte, error) {
	u := fmt.Sprintf("%s/api/versioner/v1/full/%s/title-%d.xml", c.base, url.PathEscape(date), title)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "ecfr-analytics/1.0 (contact: you@example.com)")
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("GET %s: status=%d body=%q", u, res.StatusCode, string(b))
	}
	return io.ReadAll(res.Body)
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ecfr-analytics/1.0 (contact: you@example.com)")
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("GET %s: status=%d body=%q", u, res.StatusCode, string(b))
	}
	return json.NewDecoder(res.Body).Decode(out)
}
