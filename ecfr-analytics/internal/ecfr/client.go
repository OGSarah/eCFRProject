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
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &Client{
		base: base,
		hc:   &http.Client{Timeout: timeout, Transport: tr},
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
	res, err := c.do(req)
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

func (c *Client) GetFullTitleXMLStream(ctx context.Context, date string, title int) (io.ReadCloser, error) {
	u := fmt.Sprintf("%s/api/versioner/v1/full/%s/title-%d.xml", c.base, url.PathEscape(date), title)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "ecfr-analytics/1.0 (contact: you@example.com)")
	res, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		_ = res.Body.Close()
		return nil, fmt.Errorf("GET %s: status=%d body=%q", u, res.StatusCode, string(b))
	}
	return res.Body, nil
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ecfr-analytics/1.0 (contact: you@example.com)")
	res, err := c.do(req)
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

func (c *Client) do(req *http.Request) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		r := req.Clone(req.Context())
		res, err := c.hc.Do(r)
		if err == nil {
			if res.StatusCode == 429 || res.StatusCode == 500 || res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504 {
				_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 32*1024))
				_ = res.Body.Close()
				lastErr = fmt.Errorf("GET %s: status=%d", r.URL.String(), res.StatusCode)
			} else {
				return res, nil
			}
		} else {
			lastErr = err
		}
		if attempt < maxAttempts-1 {
			delay := time.Duration(250*(1<<attempt)) * time.Millisecond
			t := time.NewTimer(delay)
			select {
			case <-req.Context().Done():
				t.Stop()
				return nil, req.Context().Err()
			case <-t.C:
			}
		}
	}
	return nil, lastErr
}
