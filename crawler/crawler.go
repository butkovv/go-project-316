package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

type Options struct {
	URL         string
	Depth       int
	Retries     int
	Delay       time.Duration
	Timeout     time.Duration
	UserAgent   string
	Concurrency int
	IndentJSON  int
	HTTPClient  *http.Client
}

type Link struct {
	URL   string
	Depth int
}

type BrokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error"`
}

type Page struct {
	URL          string       `json:"url"`
	Depth        int          `json:"depth"`
	HTTPStatus   int          `json:"http_status"`
	Status       string       `json:"status"`
	BrokenLinks  []BrokenLink `json:"broken_links"`
	DiscoveredAt time.Time    `json:"discovered_at"`
}

type Report struct {
	RootURL     string    `json:"root_url"`
	Depth       int       `json:"depth"`
	GeneratedAt time.Time `json:"generated_at"`
	Pages       []Page    `json:"pages"`
}

type Crawler struct {
	visited    map[string]bool
	httpclient *http.Client
	mu         sync.Mutex
}

func (c *Crawler) crawl(ctx context.Context, sem chan struct{}, maxDepth int, link Link) (Page, []Link, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if err != nil {
		return Page{}, nil, err
	}

	response, err := c.doRequest(ctx, sem, req)
	if err != nil {
		return Page{}, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	doc, err := html.Parse(response.Body)
	if err != nil {
		return Page{}, nil, err
	}

	page := Page{
		URL:          link.URL,
		Depth:        link.Depth,
		HTTPStatus:   response.StatusCode,
		Status:       response.Status,
		DiscoveredAt: time.Now(),
	}

	foundLinks := c.findLinks(doc, link.Depth+1, nil, link.URL)

	for _, l := range foundLinks {
		brokenLink, ok := c.checkBrokenLink(ctx, sem, l)
		if ok {
			page.BrokenLinks = append(page.BrokenLinks, brokenLink)
		}
	}

	newLinks := []Link{}
	if link.Depth < maxDepth {
		newLinks = foundLinks
	}
	return page, newLinks, nil
}

func (c *Crawler) findLinks(n *html.Node, depth int, links []Link, baseUrl string) []Link {
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key != "href" {
				continue
			}
			if strings.TrimSpace(a.Val) == "" {
				continue
			}
			u, err := url.Parse(a.Val)
			if err != nil {
				continue
			}
			if u.Scheme == "" && u.Host == "" && u.Path == "" {
				continue
			}

			link := Link{Depth: depth}

			if !u.IsAbs() {
				base, err := url.Parse(baseUrl)
				if err != nil {
					continue
				}

				link.URL = base.ResolveReference(u).String()
			} else {
				link.URL = a.Val
			}
			links = append(links, link)
		}
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		links = c.findLinks(child, depth, links, baseUrl)
	}

	return links
}

func (c *Crawler) markVisited(URL string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.visited[URL] {
		return false
	}
	c.visited[URL] = true
	return true
}

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

func (c *Crawler) checkBrokenLink(ctx context.Context, sem chan struct{}, link Link) (BrokenLink, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, link.URL, nil)
	if err != nil {
		return BrokenLink{URL: link.URL, Error: err.Error()}, true
	}
	response, err := c.doRequest(ctx, sem, req)
	if err != nil {
		return BrokenLink{URL: link.URL, Error: err.Error()}, true
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= http.StatusBadRequest {
		return BrokenLink{URL: link.URL, StatusCode: response.StatusCode}, true
	}
	return BrokenLink{}, false
}

func (c *Crawler) doRequest(ctx context.Context, sem chan struct{}, req *http.Request) (*http.Response, error) {
	err := acquire(ctx, sem)
	if err != nil {
		return nil, err
	}

	response, err := c.httpclient.Do(req)

	release(sem)

	return response, err
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

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	workers := max(opts.Concurrency, 1)

	pages := make(chan Page, workers)
	sem := make(chan struct{}, workers)

	initialDomain, err := getDomain(opts.URL)
	if err != nil {
		return []byte{}, err
	}

	var wg sync.WaitGroup
	c := &Crawler{
		httpclient: opts.HTTPClient,
		visited:    make(map[string]bool),
	}

	var crawl func(link Link)

	crawl = func(link Link) {
		defer wg.Done()
		if !c.markVisited(link.URL) {
			return
		}

		page, newLinks, err := c.crawl(ctx, sem, opts.Depth, link)

		if err != nil {
			return
		}

		select {
		case pages <- page:
		case <-ctx.Done():
			return
		}

		if link.Depth >= opts.Depth {
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
	go crawl(Link{URL: opts.URL, Depth: 0})

	go func() {
		wg.Wait()
		close(pages)
	}()

	report := Report{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now(),
		Pages:       []Page{},
	}

	for p := range pages {
		report.Pages = append(report.Pages, p)
	}

	json, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return []byte{}, err
	}
	return json, nil
}
