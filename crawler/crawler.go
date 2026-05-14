package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

type SEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
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
	SEO          SEO          `json:"seo"`
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

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return Page{}, nil, err
	}
	seo := c.getSEO(bodyBytes)

	page := Page{
		URL:          link.URL,
		Depth:        link.Depth,
		HTTPStatus:   response.StatusCode,
		Status:       response.Status,
		DiscoveredAt: time.Now(),
		SEO:          seo,
	}

	doc, err := html.Parse(bytes.NewReader(bodyBytes))
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
	baseParsed, err := url.Parse(baseUrl)
	if err != nil {
		return links
	}

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
				link.URL = baseParsed.ResolveReference(u).String()
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

func (c *Crawler) getSEO(bodyBytes []byte) SEO {
	tokenizer := html.NewTokenizer(bytes.NewReader(bodyBytes))
	seo := SEO{}

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}

		token := tokenizer.Token()
		if token.Data == "meta" {
			for _, attr := range token.Attr {
				if attr.Key == "name" && strings.ToLower(attr.Val) == "description" {
					seo.HasDescription = true
				}
				if attr.Key == "content" {
					seo.Description = cleanText(attr.Val)
				}
			}
		}

		if tokenType == html.StartTagToken && (token.Data == "title") {
			seo.HasTitle = true
			if tokenizer.Next() == html.TextToken {
				seo.Title = cleanText(tokenizer.Token().Data)
			}
		}
		if tokenType == html.StartTagToken && (token.Data == "h1") {
			seo.HasH1 = true
		}
	}
	return seo
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
