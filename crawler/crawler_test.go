package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

	_, err := Analyze(context.Background(), baseOpts(server.URL, client))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestAnalyze_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Close()

	_, err := Analyze(context.Background(), baseOpts(server.URL, server.Client()))
	if err == nil {
		t.Fatal("expected network error, got nil")
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

func TestAnalyze_InvalidURL(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}

	_, err := Analyze(context.Background(), baseOpts("http://invalid-host.local", client))
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}
