package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

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
	opts.Depth = 2
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
	if len(root.Assets) != 1 {
		t.Fatalf("root assets length = %d, want 1", len(root.Assets))
	}
	if len(child.Assets) != 1 {
		t.Fatalf("child assets length = %d, want 1", len(child.Assets))
	}
	if root.Assets[0] != child.Assets[0] {
		t.Fatalf("cached asset info differs: root=%+v child=%+v", root.Assets[0], child.Assets[0])
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
	assets := report.Pages[0].Assets
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
	assets := report.Pages[0].Assets
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
