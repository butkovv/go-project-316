package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAnalyze_JSONMatchesGoldenReport(t *testing.T) {
	server, client := goldenReportServer(t)
	defer server.Close()

	opts := baseOpts("http://source.com", client)
	opts.Depth = 1

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := string(normalizeReportJSON(t, result))
	want := `{"root_url":"https://example.com","depth":1,"generated_at":"2024-06-01T12:34:56Z","pages":[{"url":"https://example.com","depth":0,"http_status":200,"status":"ok","seo":{"has_title":true,"title":"Example title","has_description":true,"description":"Example description","has_h1":true},"broken_links":[{"url":"https://example.com/missing","status_code":404,"error":"Not Found"}],"assets":[{"url":"https://example.com/static/logo.png","type":"image","status_code":200,"size_bytes":12345}],"discovered_at":"2024-06-01T12:34:56Z"}]}`
	if got != want {
		t.Fatalf("JSON report mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestAnalyze_IndentJSONChangesOnlyFormatting(t *testing.T) {
	server, client := goldenReportServer(t)
	defer server.Close()

	compactOpts := baseOpts("http://source.com", client)
	compactOpts.Depth = 1
	compact, err := Analyze(context.Background(), compactOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	indentedOpts := compactOpts
	indentedOpts.IndentJSON = true
	indented, err := Analyze(context.Background(), indentedOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(indented, []byte("\n  \"")) {
		t.Fatalf("indented JSON has no expected indentation: %s", indented)
	}
	if bytes.Contains(compact, []byte("\n")) {
		t.Fatalf("compact JSON contains newline: %s", compact)
	}
	if !bytes.Equal(normalizeReportJSON(t, compact), normalizeReportJSON(t, indented)) {
		t.Fatalf("IndentJSON changed report content\ncompact:  %s\nindented: %s", compact, indented)
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
	if report.Pages[0].Status != "error" {
		t.Errorf("Status = %q, want %q", report.Pages[0].Status, "error")
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
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}
	if report.Pages[0].Status != "error" {
		t.Fatalf("Page status = %q, want error", report.Pages[0].Status)
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
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}
	if report.Pages[0].Status != "error" {
		t.Fatalf("Page status = %q, want error", report.Pages[0].Status)
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
	if len(report.Pages) != 1 {
		t.Fatalf("Pages length = %d, want 1", len(report.Pages))
	}
	if report.Pages[0].Status != "error" {
		t.Fatalf("Page status = %q, want error", report.Pages[0].Status)
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
			name:  "depth one includes only root",
			depth: 1,
			wantDepth: map[string]int{
				"http://source.com": 0,
			},
		},
		{
			name:  "depth two includes one transition",
			depth: 2,
			wantDepth: map[string]int{
				"http://source.com":       0,
				"http://source.com/child": 1,
			},
		},
		{
			name:  "depth three includes two transitions",
			depth: 3,
			wantDepth: map[string]int{
				"http://source.com":            0,
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
	opts.Depth = 2

	result, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	wantPages := map[string]bool{
		"http://source.com":        true,
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
	opts.Depth = 2

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
	opts.Depth = 2
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
	opts.Depth = 2
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
	opts.Depth = 2
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
