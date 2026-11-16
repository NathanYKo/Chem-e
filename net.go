package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// Client is a small HTTP helper: browser-like headers, retries with backoff on
// 429/5xx/network errors, and a global cap on in-flight requests.
type Client struct {
	HTTP    *http.Client
	sem     chan struct{}
	Retries int
	Backoff time.Duration
}

func NewClient(concurrency int, timeout time.Duration) *Client {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Client{
		HTTP:    &http.Client{Timeout: timeout},
		sem:     make(chan struct{}, concurrency),
		Retries: 3,
		Backoff: time.Second,
	}
}

type httpError struct {
	Status int
	URL    string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d from %s", e.Status, e.URL) }

func (c *Client) do(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.Backoff * time.Duration(1<<(attempt-1))):
			}
		}
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		c.sem <- struct{}{}
		resp, err := c.HTTP.Do(req)
		<-c.sem
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = &httpError{resp.StatusCode, url}
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, &httpError{resp.StatusCode, url} // 4xx won't fix itself
		}
		return data, nil
	}
	return nil, lastErr
}

func (c *Client) GetJSON(ctx context.Context, url string, out any) error {
	data, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func (c *Client) PostJSON(ctx context.Context, url string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	data, err := c.do(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}
