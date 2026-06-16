package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"listing-archiver/internal/cookies"
)

const maxPageBytes = 32 << 20

type ClientConfig struct {
	Timeout     time.Duration
	UserAgent   string
	CookiesFile string
}

type Client struct {
	HTTP           *http.Client
	UserAgent      string
	SkippedCookies int
	RawCookies     int
}

func NewClient(config ClientConfig) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	var skippedCookies int
	var rawCookies []cookies.RawCookie
	if config.CookiesFile != "" {
		loaded, err := cookies.LoadFileWithReport(config.CookiesFile, jar)
		if err != nil {
			return nil, fmt.Errorf("load cookies: %w", err)
		}
		skippedCookies = loaded.Report.Skipped
		rawCookies = loaded.Raw
	}
	client := &http.Client{
		Jar:       jar,
		Timeout:   config.Timeout,
		Transport: cookies.Transport(http.DefaultTransport, rawCookies),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("refuse redirect to unsupported scheme %q", req.URL.Scheme)
			}
			if req.URL.User != nil {
				return errors.New("refuse redirect URL containing embedded credentials")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("refuse HTTPS downgrade redirect")
			}
			setHeaders(req, config.UserAgent, "*/*")
			return nil
		},
	}
	return &Client{HTTP: client, UserAgent: config.UserAgent, SkippedCookies: skippedCookies, RawCookies: len(rawCookies)}, nil
}

func (c *Client) FetchPage(ctx context.Context, rawURL string) ([]byte, string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("create page request: %w", err)
		}
		setHeaders(req, c.UserAgent, "text/html,application/xhtml+xml")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			lastErr = statusError(resp.StatusCode, resp.Status, resp.Header, c.SkippedCookies)
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				break
			}
			continue
		}
		body, readErr := readLimited(resp.Body, maxPageBytes)
		resp.Body.Close()
		if readErr != nil {
			return nil, "", fmt.Errorf("read page: %w", readErr)
		}
		return body, resp.Request.URL.String(), nil
	}
	return nil, "", fmt.Errorf("fetch page: %w", lastErr)
}

func statusError(statusCode int, status string, header http.Header, skippedCookies int) error {
	if strings.EqualFold(header.Get("CF-Mitigated"), "challenge") {
		return fmt.Errorf("HTTP %s: Cloudflare returned a browser challenge that requires JavaScript/browser state; listing-archiver will not bypass anti-bot challenges", status)
	}
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		skipped := ""
		if skippedCookies > 0 {
			skipped = fmt.Sprintf("; %d cookie record(s) could not be safely scoped or serialized and were skipped", skippedCookies)
		}
		return fmt.Errorf("HTTP %s: server refused access; verify that exported cookies are current and access is authorized%s; listing-archiver will not bypass access controls or anti-bot challenges", status, skipped)
	case http.StatusTooManyRequests:
		return fmt.Errorf("HTTP %s: server rate limit reached", status)
	default:
		return fmt.Errorf("HTTP %s", status)
	}
}

func setHeaders(req *http.Request, userAgent, accept string) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func SafeURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return errors.New("URL has no host")
	}
	if u.User != nil {
		return errors.New("URLs containing embedded credentials are not allowed")
	}
	return nil
}

func DisplayURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid URL>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
