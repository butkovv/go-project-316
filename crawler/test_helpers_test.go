package crawler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type rewriteTransport struct {
	targets map[string]*url.URL
	base    http.RoundTripper
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ok := t.targets[req.URL.Host]
	if !ok {
		return t.base.RoundTrip(req)
	}

	rewritten := req.Clone(req.Context())
	rewritten.URL.Scheme = target.Scheme
	rewritten.URL.Host = target.Host
	return t.base.RoundTrip(rewritten)
}

func routedClient(targets map[string]string) *http.Client {
	parsedTargets := make(map[string]*url.URL, len(targets))
	for host, target := range targets {
		parsed, err := url.Parse(target)
		if err != nil {
			panic(err)
		}
		parsedTargets[host] = parsed
	}

	return &http.Client{
		Transport: rewriteTransport{targets: parsedTargets, base: http.DefaultTransport},
		Timeout:   2 * time.Second,
	}
}

type recordingTransport struct {
	targets map[string]*url.URL
	base    http.RoundTripper
	mu      sync.Mutex
	times   []time.Time
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.times = append(t.times, time.Now())
	t.mu.Unlock()

	target, ok := t.targets[req.URL.Host]
	if !ok {
		return t.base.RoundTrip(req)
	}

	rewritten := req.Clone(req.Context())
	rewritten.URL.Scheme = target.Scheme
	rewritten.URL.Host = target.Host
	return t.base.RoundTrip(rewritten)
}

func (t *recordingTransport) Times() []time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	times := make([]time.Time, len(t.times))
	copy(times, t.times)
	return times
}

func recordingRoutedClient(targets map[string]string) (*http.Client, *recordingTransport) {
	parsedTargets := make(map[string]*url.URL, len(targets))
	for host, target := range targets {
		parsed, err := url.Parse(target)
		if err != nil {
			panic(err)
		}
		parsedTargets[host] = parsed
	}

	transport := &recordingTransport{targets: parsedTargets, base: http.DefaultTransport}
	return &http.Client{Transport: transport, Timeout: 2 * time.Second}, transport
}

func baseOpts(url string, client *http.Client) Options {
	return Options{
		URL:        url,
		HTTPClient: client,
	}
}

func normalizeReportJSON(t *testing.T, data []byte) []byte {
	t.Helper()

	fixedTime := time.Date(2024, 6, 1, 12, 34, 56, 0, time.UTC)
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	report.RootURL = "https://example.com"
	report.GeneratedAt = fixedTime
	for i := range report.Pages {
		report.Pages[i].URL = "https://example.com"
		report.Pages[i].DiscoveredAt = fixedTime
		for j := range report.Pages[i].BrokenLinks {
			report.Pages[i].BrokenLinks[j].URL = "https://example.com/missing"
		}
		for j := range report.Pages[i].Assets {
			report.Pages[i].Assets[j].URL = "https://example.com/static/logo.png"
		}
	}

	normalized, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal normalized report: %v", err)
	}
	return normalized
}

func goldenReportServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`
				<html>
					<head>
						<title>Example title</title>
						<meta name="description" content="Example description">
					</head>
					<body>
						<h1>Heading</h1>
						<a href="http://external.com/missing">missing</a>
						<img src="/static/logo.png">
					</body>
				</html>`))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/static/logo.png":
			w.Header().Set("Content-Length", "12345")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	client := routedClient(map[string]string{
		"source.com":   server.URL,
		"external.com": server.URL,
	})

	return server, client
}
