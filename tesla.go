package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const teslaStateURL = "https://www.tesla.com/cua-api/apps/careers/state"

// fetchTesla reads Tesla's public careers catalogue. Akamai rejects the plain
// HTTP client, so a 403 falls back to a real Chrome window, which is the
// session that catalogue actually answers.
func fetchTesla(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	body, err := c.do(ctx, http.MethodGet, teslaStateURL, nil)
	if err != nil || !json.Valid(body) {
		if err != nil && !teslaBlocked(err) {
			return nil, err
		}
		body, err = fetchTeslaViaChrome(ctx)
		if err != nil {
			return nil, err
		}
	}
	return parseTesla(body, co)
}

func teslaBlocked(err error) bool {
	var he *httpError
	return errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusServiceUnavailable)
}

func parseTesla(body []byte, co Company) ([]Job, error) {
	var r struct {
		Lookup struct {
			Locations map[string]string `json:"locations"`
		} `json:"lookup"`
		Listings []struct {
			ID    string `json:"id"`
			Title string `json:"t"`
			Loc   string `json:"l"`
		} `json:"listings"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("tesla: %w", err)
	}
	if len(r.Listings) == 0 {
		return nil, fmt.Errorf("tesla: catalogue was empty")
	}
	out := make([]Job, 0, len(r.Listings))
	for _, p := range r.Listings {
		title := strings.TrimSpace(p.Title)
		if p.ID == "" || title == "" {
			continue
		}
		out = append(out, Job{
			ID:       "tesla:" + p.ID,
			Company:  co.Name,
			Category: co.Category,
			Title:    title,
			Location: r.Lookup.Locations[p.Loc],
			URL:      teslaJobURL(p.Title, p.ID),
			Source:   "tesla",
		})
	}
	return out, nil
}

// teslaJobURL matches Tesla's public path. A leading space in the title
// becomes a leading hyphen, and trailing punctuation is dropped before the id.
func teslaJobURL(title, id string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			hyphen = false
			continue
		}
		if !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	return "https://www.tesla.com/careers/search/job/" + slug + "-" + id
}

func fetchTeslaViaChrome(ctx context.Context) ([]byte, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(900, 700),
		chromedp.Flag("window-position", "-2400,-2400"),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()
	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	defer cancelTask()
	taskCtx, cancelTimeout := context.WithTimeout(taskCtx, 50*time.Second)
	defer cancelTimeout()

	var raw string
	err := chromedp.Run(taskCtx,
		chromedp.Navigate("https://www.tesla.com/careers/search/"),
		chromedp.Sleep(7*time.Second),
		chromedp.Evaluate(
			`fetch('/cua-api/apps/careers/state',{headers:{accept:'application/json'}}).then(r => r.text())`,
			&raw,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithAwaitPromise(true)
			},
		),
	)
	if err != nil {
		return nil, fmt.Errorf("tesla chrome: %w", err)
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("tesla chrome: catalogue was not JSON")
	}
	return []byte(raw), nil
}
