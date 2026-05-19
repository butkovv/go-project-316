package crawler

import (
	"context"
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

func TestAnalyze_Success200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if report.RootURL != server.URL {
		t.Errorf("RootURL = %q, want %q", report.RootURL, server.URL)
	}
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}
	page := report.Pages[0]
	if page.HTTPStatus != 200 {
		t.Errorf("HTTPStatus = %d, want 200", page.HTTPStatus)
	}
	if page.URL != server.URL {
		t.Errorf("Page URL = %q, want %q", page.URL, server.URL)
	}
	if page.Depth != 0 {
		t.Errorf("Page Depth = %d, want 0", page.Depth)
	}
}

func TestAnalyze_404NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if report.Pages[0].HTTPStatus != 404 {
		t.Errorf("HTTPStatus = %d, want 404", report.Pages[0].HTTPStatus)
	}
	if report.Pages[0].Status != "404 Not Found" {
		t.Errorf("Status = %q, want %q", report.Pages[0].Status, "404 Not Found")
	}
}

func TestAnalyze_500InternalServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if report.Pages[0].HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %d, want 500", report.Pages[0].HTTPStatus)
	}
}

func TestAnalyze_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 50 * time.Millisecond}

	result, err := Analyze(context.Background(), baseOpts(server.URL, client))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 0 {
		t.Fatalf("Pages length = %d, want 0", len(report.Pages))
	}
}

func TestAnalyze_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 0 {
		t.Fatalf("Pages length = %d, want 0", len(report.Pages))
	}
}

func TestAnalyze_BrokenLinkDetected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body><a href="/ok">ok</a><a href="/broken">broken</a></body></html>`))
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/broken":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Concurrency = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}

	page := report.Pages[0]
	if page.HTTPStatus != 200 {
		t.Errorf("Page HTTPStatus = %d, want 200", page.HTTPStatus)
	}

	if len(page.BrokenLinks) != 1 {
		t.Fatalf("BrokenLinks length = %d, want 1", len(page.BrokenLinks))
	}

	bl := page.BrokenLinks[0]
	if bl.URL != server.URL+"/broken" {
		t.Errorf("BrokenLink URL = %q, want %q", bl.URL, server.URL+"/broken")
	}
	if bl.StatusCode != 404 {
		t.Errorf("BrokenLink StatusCode = %d, want 404", bl.StatusCode)
	}
}

func TestAnalyze_SEOElementsPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`
			<html>
				<head>
					<title>Fish &amp; Chips</title>
					<meta name="description" content="Fresh &amp; tasty seafood">
				</head>
				<body>
					<h1>Main Heading</h1>
				</body>
			</html>
		`))
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}

	seo := report.Pages[0].SEO
	if !seo.HasTitle {
		t.Error("SEO.HasTitle = false, want true")
	}
	if seo.Title != "Fish & Chips" {
		t.Errorf("SEO.Title = %q, want %q", seo.Title, "Fish & Chips")
	}
	if !seo.HasDescription {
		t.Error("SEO.HasDescription = false, want true")
	}
	if seo.Description != "Fresh & tasty seafood" {
		t.Errorf("SEO.Description = %q, want %q", seo.Description, "Fresh & tasty seafood")
	}
	if !seo.HasH1 {
		t.Error("SEO.HasH1 = false, want true")
	}
}

func TestAnalyze_SEOElementsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><head></head><body><p>No SEO here</p></body></html>`))
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}

	seo := report.Pages[0].SEO
	if seo.HasTitle {
		t.Error("SEO.HasTitle = true, want false")
	}
	if seo.Title != "" {
		t.Errorf("SEO.Title = %q, want empty", seo.Title)
	}
	if seo.HasDescription {
		t.Error("SEO.HasDescription = true, want false")
	}
	if seo.Description != "" {
		t.Errorf("SEO.Description = %q, want empty", seo.Description)
	}
	if seo.HasH1 {
		t.Error("SEO.HasH1 = true, want false")
	}
}

func TestAnalyze_SEOTextIsCleaned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`
			<html>
				<head>
					<title>
						Fish   &amp;
						Chips
					</title>
					<meta name="description" content=" Fresh   &amp; tasty   seafood ">
				</head>
				<body>
					<h1>Main Heading</h1>
				</body>
			</html>
		`))
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}

	seo := report.Pages[0].SEO
	if seo.Title != "Fish & Chips" {
		t.Errorf("SEO.Title = %q, want %q", seo.Title, "Fish & Chips")
	}
	if seo.Description != "Fresh & tasty seafood" {
		t.Errorf("SEO.Description = %q, want %q", seo.Description, "Fresh & tasty seafood")
	}
}

func TestAnalyze_DepthLimitsTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><body><a href="/child">child</a></body></html>`))
		case "/child":
			_, _ = w.Write([]byte(`<html><body><a href="/grandchild">grandchild</a></body></html>`))
		case "/grandchild":
			_, _ = w.Write([]byte(`<html><body>grandchild</body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := routedClient(map[string]string{"source.com": server.URL})
	tests := []struct {
		name      string
		depth     int
		wantDepth map[string]int
	}{
		{
			name:  "depth zero includes only root",
			depth: 0,
			wantDepth: map[string]int{
				"http://source.com": 0,
			},
		},
		{
			name:  "depth one includes one transition",
			depth: 1,
			wantDepth: map[string]int{
				"http://source.com":        0,
				"http://source.com/child": 1,
			},
		},
		{
			name:  "depth two includes two transitions",
			depth: 2,
			wantDepth: map[string]int{
				"http://source.com":             0,
				"http://source.com/child":      1,
				"http://source.com/grandchild": 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := baseOpts("http://source.com", client)
			opts.Depth = tt.depth

			result, err := Analyze(context.Background(), opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var report Report
			if err := json.Unmarshal(result, &report); err != nil {
				t.Fatalf("invalid json: %v", err)
			}

			if len(report.Pages) != len(tt.wantDepth) {
				t.Fatalf("Pages length = %d, want %d", len(report.Pages), len(tt.wantDepth))
			}

			for _, page := range report.Pages {
				want, ok := tt.wantDepth[page.URL]
				if !ok {
					t.Fatalf("unexpected page in report: %s", page.URL)
				}
				if page.Depth != want {
					t.Errorf("Page %s depth = %d, want %d", page.URL, page.Depth, want)
				}
			}
		})
	}
}

func TestAnalyze_ExternalLinksAreNotCrawled(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`
				<html><body>
					<a href="/first">first</a>
					<a href="/second">second</a>
					<a href="http://external.com/outside">external</a>
				</body></html>`))
		case "/first", "/second":
			_, _ = w.Write([]byte(`<html><body>internal</body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer sourceServer.Close()

	externalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer externalServer.Close()

	client := routedClient(map[string]string{
		"source.com":   sourceServer.URL,
		"external.com": externalServer.URL,
	})
	opts := baseOpts("http://source.com", client)
	opts.Depth = 1

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	wantPages := map[string]bool{
		"http://source.com":         true,
		"http://source.com/first":  true,
		"http://source.com/second": true,
	}
	if len(report.Pages) != len(wantPages) {
		t.Fatalf("Pages length = %d, want %d", len(report.Pages), len(wantPages))
	}
	for _, page := range report.Pages {
		if !wantPages[page.URL] {
			t.Fatalf("unexpected page in report: %s", page.URL)
		}
		if page.URL == "http://external.com/outside" {
			t.Fatal("external page appeared in report pages")
		}
	}
}

func TestAnalyze_DuplicateLinksAppearOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><body><a href="/same">same</a><a href="/same">same again</a></body></html>`))
		case "/same":
			_, _ = w.Write([]byte(`<html><body>same</body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := routedClient(map[string]string{"source.com": server.URL})
	opts := baseOpts("http://source.com", client)
	opts.Depth = 1

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	counts := map[string]int{}
	for _, page := range report.Pages {
		counts[page.URL]++
	}

	if counts["http://source.com"] != 1 {
		t.Errorf("root page count = %d, want 1", counts["http://source.com"])
	}
	if counts["http://source.com/same"] != 1 {
		t.Errorf("duplicate target page count = %d, want 1", counts["http://source.com/same"])
	}
	if len(report.Pages) != 2 {
		t.Fatalf("Pages length = %d, want 2", len(report.Pages))
	}
}

func TestAnalyze_DuplicateAssetFetchedOnce(t *testing.T) {
	var mu sync.Mutex
	assetRequests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><body><a href="/child">child</a><img src="/static/logo.png"></body></html>`))
		case "/child":
			_, _ = w.Write([]byte(`<html><body><img src="/static/logo.png"></body></html>`))
		case "/static/logo.png":
			mu.Lock()
			assetRequests++
			mu.Unlock()
			w.Header().Set("Content-Length", "12345")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Depth = 1
	opts.Concurrency = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 2 {
		t.Fatalf("Pages length = %d, want 2", len(report.Pages))
	}

	pages := map[string]Page{}
	for _, page := range report.Pages {
		pages[page.URL] = page
	}
	root := pages[server.URL]
	child := pages[server.URL+"/child"]
	if len(root.AssetsInfo) != 1 {
		t.Fatalf("root assets length = %d, want 1", len(root.AssetsInfo))
	}
	if len(child.AssetsInfo) != 1 {
		t.Fatalf("child assets length = %d, want 1", len(child.AssetsInfo))
	}
	if root.AssetsInfo[0] != child.AssetsInfo[0] {
		t.Fatalf("cached asset info differs: root=%+v child=%+v", root.AssetsInfo[0], child.AssetsInfo[0])
	}

	mu.Lock()
	gotRequests := assetRequests
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("asset request count = %d, want 1", gotRequests)
	}
}

func TestAnalyze_AssetSizeFromBodyWithoutContentLength(t *testing.T) {
	body := []byte("asset body without declared size")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><script src="/static/app.js"></script></body></html>`))
		case "/static/app.js":
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}
	assets := report.Pages[0].AssetsInfo
	if len(assets) != 1 {
		t.Fatalf("Assets length = %d, want 1", len(assets))
	}
	if assets[0].SizeBytes != int64(len(body)) {
		t.Fatalf("asset size_bytes = %d, want %d", assets[0].SizeBytes, len(body))
	}
	if assets[0].Type != AssetTypeScript {
		t.Fatalf("asset type = %q, want %q", assets[0].Type, AssetTypeScript)
	}
}

func TestAnalyze_AssetHTTPErrorReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><link rel="stylesheet" href="/static/missing.css"></head><body></body></html>`))
		case "/static/missing.css":
			w.Header().Set("Content-Length", "9")
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}
	assets := report.Pages[0].AssetsInfo
	if len(assets) != 1 {
		t.Fatalf("Assets length = %d, want 1", len(assets))
	}
	asset := assets[0]
	if asset.StatusCode != http.StatusNotFound {
		t.Fatalf("asset status_code = %d, want %d", asset.StatusCode, http.StatusNotFound)
	}
	if asset.Error == "" {
		t.Fatal("asset error is empty, want response status")
	}
	if asset.Type != AssetTypeStyle {
		t.Fatalf("asset type = %q, want %q", asset.Type, AssetTypeStyle)
	}

	var raw map[string]any
	if err := json.Unmarshal(result, &raw); err != nil {
		t.Fatalf("invalid raw json: %v", err)
	}
	rawPages := raw["pages"].([]any)
	rawPage := rawPages[0].(map[string]any)
	rawAssets := rawPage["assets"].([]any)
	rawAsset := rawAssets[0].(map[string]any)
	for _, field := range []string{"url", "type", "status_code", "size_bytes", "error"} {
		if _, ok := rawAsset[field]; !ok {
			t.Fatalf("asset json field %q is missing", field)
		}
	}
}

func TestAnalyze_DelayLimitsRequestIntervals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><body><a href="/first">first</a><a href="/second">second</a></body></html>`))
		case "/first", "/second":
			_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, transport := recordingRoutedClient(map[string]string{"source.com": server.URL})
	opts := baseOpts("http://source.com", client)
	opts.Depth = 1
	opts.Concurrency = 4
	opts.Delay = 40 * time.Millisecond

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 3 {
		t.Fatalf("Pages length = %d, want 3", len(report.Pages))
	}

	times := transport.Times()
	if len(times) < 2 {
		t.Fatalf("recorded request count = %d, want at least 2", len(times))
	}
	for i := 1; i < len(times); i++ {
		interval := times[i].Sub(times[i-1])
		if interval < 30*time.Millisecond {
			t.Fatalf("request interval %d = %s, want at least %s", i, interval, 30*time.Millisecond)
		}
	}
}

func TestAnalyze_RPSOverridesDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><body><a href="/first">first</a><a href="/second">second</a></body></html>`))
		case "/first", "/second":
			_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, transport := recordingRoutedClient(map[string]string{"source.com": server.URL})
	opts := baseOpts("http://source.com", client)
	opts.Depth = 1
	opts.Concurrency = 4
	opts.Delay = 500 * time.Millisecond
	opts.RPS = 20

	started := time.Now()
	result, err := Analyze(context.Background(), opts)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 3 {
		t.Fatalf("Pages length = %d, want 3", len(report.Pages))
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("Analyze elapsed = %s, want less than delay %s when RPS is set", elapsed, 500*time.Millisecond)
	}

	times := transport.Times()
	if len(times) < 2 {
		t.Fatalf("recorded request count = %d, want at least 2", len(times))
	}
	for i := 1; i < len(times); i++ {
		interval := times[i].Sub(times[i-1])
		if interval < 30*time.Millisecond {
			t.Fatalf("request interval %d = %s, want at least %s", i, interval, 30*time.Millisecond)
		}
	}
}

func TestAnalyze_NoRateLimitDoesNotDelayReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><body><a href="/first">first</a><a href="/second">second</a></body></html>`))
		case "/first", "/second":
			_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := routedClient(map[string]string{"source.com": server.URL})
	opts := baseOpts("http://source.com", client)
	opts.Depth = 1
	opts.Concurrency = 4

	started := time.Now()
	result, err := Analyze(context.Background(), opts)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("Analyze elapsed = %s, want no artificial rate-limit delay", elapsed)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 3 {
		t.Fatalf("Pages length = %d, want 3", len(report.Pages))
	}
}

func TestAnalyze_ContextCancelStopsRateLimitWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := baseOpts(server.URL, server.Client())
	opts.Delay = 5 * time.Second

	started := time.Now()
	result, err := Analyze(ctx, opts)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("Analyze elapsed = %s, want context cancellation to stop rate wait quickly", elapsed)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json after context cancellation: %v", err)
	}
}

func TestAnalyze_RetriesFinalFailureReportedInBrokenLinks(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	statuses := []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusInternalServerError}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><a href="/flaky">flaky</a></body></html>`))
		case r.URL.Path == "/flaky" && r.Method == http.MethodHead:
			mu.Lock()
			status := statuses[requests]
			requests++
			mu.Unlock()
			w.WriteHeader(status)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Retries = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}

	brokenLinks := report.Pages[0].BrokenLinks
	if len(brokenLinks) != 1 {
		t.Fatalf("BrokenLinks length = %d, want 1", len(brokenLinks))
	}
	if brokenLinks[0].StatusCode != http.StatusInternalServerError {
		t.Errorf("final broken link status = %d, want %d", brokenLinks[0].StatusCode, http.StatusInternalServerError)
	}
	if requests != 3 {
		t.Fatalf("HEAD request count = %d, want 3", requests)
	}
}

func TestAnalyze_RetriesStopAfterSuccess(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	statuses := []int{http.StatusInternalServerError, http.StatusOK}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><a href="/flaky">flaky</a></body></html>`))
		case r.URL.Path == "/flaky" && r.Method == http.MethodHead:
			mu.Lock()
			status := statuses[requests]
			requests++
			mu.Unlock()
			w.WriteHeader(status)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Retries = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}
	if len(report.Pages[0].BrokenLinks) != 0 {
		t.Fatalf("BrokenLinks length = %d, want 0", len(report.Pages[0].BrokenLinks))
	}
	if requests != 2 {
		t.Fatalf("HEAD request count = %d, want 2", requests)
	}
}

func TestAnalyze_RetriesDoNotExceedLimit(t *testing.T) {
	var mu sync.Mutex
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><a href="/flaky">flaky</a></body></html>`))
		case r.URL.Path == "/flaky" && r.Method == http.MethodHead:
			mu.Lock()
			requests++
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Retries = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if requests > opts.Retries+1 {
		t.Fatalf("HEAD request count = %d, want at most %d", requests, opts.Retries+1)
	}
}

func TestAnalyze_RetriesSkipPermanent404(t *testing.T) {
	var mu sync.Mutex
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><a href="/missing">missing</a></body></html>`))
		case r.URL.Path == "/missing" && r.Method == http.MethodHead:
			mu.Lock()
			requests++
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Retries = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if requests != 1 {
		t.Fatalf("HEAD request count = %d, want 1", requests)
	}
	if len(report.Pages[0].BrokenLinks) != 1 {
		t.Fatalf("BrokenLinks length = %d, want 1", len(report.Pages[0].BrokenLinks))
	}
	if report.Pages[0].BrokenLinks[0].StatusCode != http.StatusNotFound {
		t.Errorf("BrokenLink status = %d, want %d", report.Pages[0].BrokenLinks[0].StatusCode, http.StatusNotFound)
	}
}

func TestAnalyze_RetriesTemporary429(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	statuses := []int{http.StatusTooManyRequests, http.StatusOK}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><a href="/limited">limited</a></body></html>`))
		case r.URL.Path == "/limited" && r.Method == http.MethodHead:
			mu.Lock()
			status := statuses[requests]
			requests++
			mu.Unlock()
			w.WriteHeader(status)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Retries = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if requests != 2 {
		t.Fatalf("HEAD request count = %d, want 2", requests)
	}
	if len(report.Pages[0].BrokenLinks) != 0 {
		t.Fatalf("BrokenLinks length = %d, want 0", len(report.Pages[0].BrokenLinks))
	}
}

func TestAnalyze_RetryContextCancelStopsAttempts(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	ctx, cancel := context.WithCancel(context.Background())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><a href="/flaky">flaky</a></body></html>`))
		case r.URL.Path == "/flaky" && r.Method == http.MethodHead:
			mu.Lock()
			requests++
			mu.Unlock()
			cancel()
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	opts := baseOpts(server.URL, server.Client())
	opts.Retries = 2

	started := time.Now()
	result, err := Analyze(ctx, opts)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("Analyze elapsed = %s, want retry cancellation to stop quickly", elapsed)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if requests != 1 {
		t.Fatalf("HEAD request count = %d, want 1", requests)
	}
}

func TestAnalyze_InvalidURL(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}

	result, err := Analyze(context.Background(), baseOpts("http://invalid-host.local", client))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(report.Pages) != 0 {
		t.Fatalf("Pages length = %d, want 0", len(report.Pages))
	}
}
