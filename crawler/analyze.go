package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Analyze crawls the configured URL and returns a JSON report.
func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	rootURL := canonicalURL(opts.URL)

	workers := max(opts.Concurrency, 1)

	pages := make(chan Page, workers)
	sem := make(chan struct{}, workers)

	var ticker *time.Ticker

	switch {
	case opts.RPS > 0:
		ticker = time.NewTicker(time.Second / time.Duration(opts.RPS))
		defer ticker.Stop()
	case opts.Delay > 0:
		ticker = time.NewTicker(opts.Delay)
		defer ticker.Stop()
	}

	initialDomain, err := getDomain(rootURL)
	if err != nil {
		return []byte{}, err
	}
	maxDepth := max(opts.Depth-1, 0)

	var wg sync.WaitGroup

	hc := *opts.HTTPClient
	baseTransport := opts.HTTPClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	hc.Transport = &RetryTransport{
		Next:       baseTransport,
		MaxRetries: opts.Retries,
		BaseDelay:  max(100*time.Millisecond, opts.Delay),
	}
	c := &Crawler{
		httpclient: &hc,
		visited:    make(map[string]bool),
		assetsCache: &AssetsCache{
			data: make(map[string]Asset),
		},
	}

	var crawl func(link Link)

	crawl = func(link Link) {
		defer wg.Done()
		if !c.markVisited(link.URL) {
			return
		}

		page, newLinks, err := c.crawl(ctx, sem, ticker, maxDepth, link)

		if err != nil {
			page = Page{
				URL:          link.URL,
				Depth:        link.Depth,
				Status:       "error",
				Error:        err.Error(),
				DiscoveredAt: time.Now(),
			}
		}

		select {
		case pages <- page:
		case <-ctx.Done():
			return
		}

		if link.Depth >= maxDepth {
			return
		}

		for _, newLink := range newLinks {
			domain, err := getDomain(newLink.URL)
			if err != nil || domain != initialDomain {
				continue
			}
			wg.Add(1)
			go crawl(newLink)
		}
	}

	wg.Add(1)
	go crawl(Link{URL: rootURL, Depth: 0})

	go func() {
		wg.Wait()
		close(pages)
	}()

	report := Report{
		RootURL:     rootURL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now(),
		Pages:       []Page{},
	}

	for p := range pages {
		report.Pages = append(report.Pages, p)
	}
	sort.SliceStable(report.Pages, func(i, j int) bool {
		if report.Pages[i].Depth != report.Pages[j].Depth {
			return report.Pages[i].Depth < report.Pages[j].Depth
		}
		if report.Pages[i].URL == rootURL {
			return true
		}
		if report.Pages[j].URL == rootURL {
			return false
		}
		return report.Pages[i].URL < report.Pages[j].URL
	})

	var output []byte

	if opts.IndentJSON {
		output, err = json.MarshalIndent(report, "", "  ")
	} else {
		output, err = json.Marshal(report)
	}

	if err != nil {
		return []byte{}, err
	}
	return output, nil
}
