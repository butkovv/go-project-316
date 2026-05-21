package crawler

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

func getDomain(rawUrl string) (string, error) {
	u, err := url.Parse(rawUrl)
	if err != nil {
		return "", err
	}
	h := u.Host
	d, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil {
		return "", err
	}
	return d, nil
}

func acquire(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(sem chan struct{}) {
	<-sem
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func pageStatus(code int) string {
	if code >= 200 && code < 300 {
		return "ok"
	}
	return "error"
}

func canonicalURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	if u.Path == "/" && u.RawQuery == "" && u.Fragment == "" {
		u.Path = ""
	}

	return u.String()
}

func (c *Crawler) doRequest(ctx context.Context, sem chan struct{}, ticker *time.Ticker, req *http.Request) (*http.Response, error) {
	if ticker != nil {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	err := acquire(ctx, sem)
	if err != nil {
		return nil, err
	}

	response, err := c.httpclient.Do(req)

	release(sem)

	return response, err
}

// RoundTrip executes an HTTP request with retry behavior for temporary failures.
func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	maxAttempts := max(t.MaxRetries, 0) + 1

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Pow(2, float64(attempt))) * t.BaseDelay

			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
			}
		}

		resp, err = t.Next.RoundTrip(req)

		if err != nil || (resp != nil && (resp.StatusCode == 429 || resp.StatusCode >= 500)) {
			if attempt < maxAttempts-1 {
				if resp != nil {
					_ = resp.Body.Close()
				}
				continue
			}
		}
		return resp, err
	}
	return resp, err
}

func newGetRequest(ctx context.Context, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}
