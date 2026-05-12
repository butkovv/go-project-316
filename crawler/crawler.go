package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/html"
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
	wg         sync.WaitGroup
}

func (c *Crawler) crawl(url string, depth int, maxDepth int, pages chan Page) {
	defer c.wg.Done()

	c.mu.Lock()
	if c.visited[url] {
		c.mu.Unlock()
		return
	}
	c.visited[url] = true
	c.mu.Unlock()

	response, err := c.httpclient.Get(url)
	if err != nil {
		fmt.Printf("error getting %s: %v", url, err)
		return
	}
	defer func() { _ = response.Body.Close() }()

	doc, err := html.Parse(response.Body)
	if err != nil {
		return
	}

	page := Page{
		URL:          url,
		Depth:        depth,
		HTTPStatus:   response.StatusCode,
		Status:       response.Status,
		DiscoveredAt: time.Now(),
	}

	links := c.findLinks(doc, []string{}, url)
	brokenLinks := make(chan BrokenLink, 16)
	var wg sync.WaitGroup
	for _, link := range links {
		wg.Add(1)
		go func(link string) {
			defer wg.Done()
			response, err := c.httpclient.Get(link)
			if err != nil {
				brokenLinks <- BrokenLink{URL: link, Error: err.Error()}
				return
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode >= http.StatusBadRequest {
				brokenLinks <- BrokenLink{URL: link, StatusCode: response.StatusCode}
			}
		}(link)
	}
	go func() {
		wg.Wait()
		close(brokenLinks)
	}()

	for b := range brokenLinks {
		page.BrokenLinks = append(page.BrokenLinks, b)
	}

	pages <- page

	if depth >= maxDepth {
		return
	} else {
		links := c.findLinks(doc, []string{}, url)

		for _, link := range links {
			c.wg.Add(1)
			go c.crawl(link, depth+1, maxDepth, pages)
		}
	}
}

func (c *Crawler) findLinks(n *html.Node, links []string, baseUrl string) []string {
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" {
				fmt.Printf("href found: %s\n", a.Val)
				u, err := url.Parse(a.Val)
				if err != nil {
					continue
				}
				if !u.IsAbs() {
					base, _ := url.Parse(baseUrl)
					abs := base.ResolveReference(u)
					links = append(links, abs.String())
				} else {
					links = append(links, a.Val)
				}
			}
		}
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		links = c.findLinks(child, links, baseUrl)
	}

	return links
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	workers := max(opts.Concurrency, 1)
	pages := make(chan Page, workers*2)
	c := &Crawler{httpclient: opts.HTTPClient, visited: make(map[string]bool)}

	c.wg.Add(1)
	go c.crawl(opts.URL, 0, opts.Depth, pages)

	report := Report{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now(),
		Pages:       []Page{},
	}

	go func() {
		c.wg.Wait()
		close(pages)
	}()

	for p := range pages {
		report.Pages = append(report.Pages, p)
	}

	json, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return []byte{}, err
	}
	return json, nil
}
