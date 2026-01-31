package ecfr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	const maxAttempts = 5
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		r := req.Clone(req.Context())
		res, err := c.hc.Do(r)
		if err == nil {
			if res.StatusCode == 429 || res.StatusCode == 500 || res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504 {
				_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 32*1024))
				_ = res.Body.Close()
				lastErr = fmt.Errorf("GET %s: status=%d", r.URL.String(), res.StatusCode)
				if attempt < maxAttempts-1 {
					if err := sleepWithRetryAfter(req.Context(), res, attempt); err != nil {
						return nil, err
					}
					continue
				}
			} else {
				return res, nil
			}
		} else {
			lastErr = err
		}
		if attempt < maxAttempts-1 {
			delay := time.Duration(500*(1<<attempt)) * time.Millisecond
			jitter := time.Duration(time.Now().UnixNano()%200) * time.Millisecond
			sleep := delay + jitter
			if err := sleepWithContext(req.Context(), sleep); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func sleepWithRetryAfter(ctx context.Context, res *http.Response, attempt int) error {
	if res.StatusCode == 429 {
		if ra := res.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				return sleepWithContext(ctx, time.Duration(secs)*time.Second)
			}
			if t, err := time.Parse(time.RFC1123, ra); err == nil {
				d := time.Until(t)
				if d < 0 {
					d = 0
				}
				return sleepWithContext(ctx, d)
			}
			if t, err := time.Parse(time.RFC1123Z, ra); err == nil {
				d := time.Until(t)
				if d < 0 {
					d = 0
				}
				return sleepWithContext(ctx, d)
			}
		}
	}
	delay := time.Duration(700*(1<<attempt)) * time.Millisecond
	jitter := time.Duration(time.Now().UnixNano()%250) * time.Millisecond
	sleep := delay + jitter
	if sleep > 12*time.Second {
		sleep = 12 * time.Second
	}
	return sleepWithContext(ctx, sleep)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
