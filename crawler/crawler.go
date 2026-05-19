package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
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
	RPS         int
	UserAgent   string
	Concurrency int
	IndentJSON  bool
	HTTPClient  *http.Client
}

type Link struct {
	URL   string
	Depth int
}

type AssetType string

type AssetLink struct {
	URL  string
	Type AssetType
}

const (
	AssetTypeImage  AssetType = "image"
	AssetTypeScript AssetType = "script"
	AssetTypeStyle  AssetType = "style"
	AssetTypeOther  AssetType = "other"
)

type Asset struct {
	URL        string    `json:"url"`
	Type       AssetType `json:"type"`
	StatusCode int       `json:"status_code"`
	SizeBytes  int64     `json:"size_bytes"`
	Error      string    `json:"error"`
}

type AssetsCache struct {
	cacheMu  sync.RWMutex
	data     map[string]Asset
	keyLocks sync.Map
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
	Error        string       `json:"error"`
	SEO          SEO          `json:"seo"`
	BrokenLinks  []BrokenLink `json:"broken_links"`
	Assets       []Asset      `json:"assets"`
	DiscoveredAt time.Time    `json:"discovered_at"`
}

type Report struct {
	RootURL     string    `json:"root_url"`
	Depth       int       `json:"depth"`
	GeneratedAt time.Time `json:"generated_at"`
	Pages       []Page    `json:"pages"`
}

type Crawler struct {
	visited     map[string]bool
	assetsCache *AssetsCache
	httpclient  *http.Client
	mu          sync.Mutex
}

type RetryTransport struct {
	Next       http.RoundTripper
	MaxRetries int
	BaseDelay  time.Duration
}

func (ac *AssetsCache) getMutex(key string) *sync.Mutex {
	newMu := &sync.Mutex{}
	actual, _ := ac.keyLocks.LoadOrStore(key, newMu)

	return actual.(*sync.Mutex)
}

func (ac *AssetsCache) GetOrFetchAsset(ctx context.Context, sem chan struct{}, ticker *time.Ticker, assetLink AssetLink, fetchFunc func(ctx context.Context, sem chan struct{}, ticker *time.Ticker, assetLink AssetLink) Asset) Asset {
	ac.cacheMu.RLock()
	val, exists := ac.data[assetLink.URL]
	ac.cacheMu.RUnlock()
	if exists {
		return val
	}

	keyMu := ac.getMutex(assetLink.URL)
	keyMu.Lock()
	defer keyMu.Unlock()

	ac.cacheMu.RLock()
	val, exists = ac.data[assetLink.URL]
	ac.cacheMu.RUnlock()
	if exists {
		return val
	}
	data := fetchFunc(ctx, sem, ticker, assetLink)
	ac.cacheMu.Lock()
	ac.data[assetLink.URL] = data
	ac.cacheMu.Unlock()

	return data
}

func (c *Crawler) crawl(ctx context.Context, sem chan struct{}, ticker *time.Ticker, maxDepth int, link Link) (Page, []Link, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if err != nil {
		return Page{}, nil, err
	}

	response, err := c.doRequest(ctx, sem, ticker, req)
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
		Status:       pageStatus(response.StatusCode),
		SEO:          seo,
		BrokenLinks:  []BrokenLink{},
		Assets:       []Asset{},
		DiscoveredAt: time.Now(),
	}

	doc, err := html.Parse(bytes.NewReader(bodyBytes))
	if err != nil {
		return Page{}, nil, err
	}

	foundLinks := c.findLinks(doc, link.Depth+1, nil, link.URL)

	for _, l := range foundLinks {
		brokenLink, ok := c.checkBrokenLink(ctx, sem, ticker, l)
		if ok {
			page.BrokenLinks = append(page.BrokenLinks, brokenLink)
		}
	}

	assetLinks := c.findAssetLinks(doc, []AssetLink{}, link.URL)
	for _, assetLink := range assetLinks {
		asset := c.assetsCache.GetOrFetchAsset(ctx, sem, ticker, assetLink, c.fetchAsset)
		page.Assets = append(page.Assets, asset)
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

func (c *Crawler) findAssetLinks(n *html.Node, assetLinks []AssetLink, baseUrl string) []AssetLink {
	baseParsed, err := url.Parse(baseUrl)
	if err != nil {
		return assetLinks
	}
	if n.Type == html.ElementNode {
		var u string
		var t AssetType
		switch n.Data {
		case "link":
			rel := getAttr(n, "rel")
			if rel == "stylesheet" {
				u = getAttr(n, "href")
				t = AssetTypeStyle
			}
		case "script":
			u = getAttr(n, "src")
			t = AssetTypeScript
		case "img":
			u = getAttr(n, "src")
			t = AssetTypeImage
		}
		u = strings.TrimSpace(u)
		if u != "" {
			parsed, err := url.Parse(u)
			if err != nil {
				return assetLinks
			}
			if parsed.Scheme == "" && parsed.Host == "" && parsed.Path == "" {
				return assetLinks
			}
			assetLink := AssetLink{Type: t}
			if !parsed.IsAbs() {
				assetLink.URL = baseParsed.ResolveReference(parsed).String()
			} else {
				assetLink.URL = u
			}
			assetLinks = append(assetLinks, assetLink)
		}
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		assetLinks = c.findAssetLinks(child, assetLinks, baseUrl)
	}

	return assetLinks
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

func (c *Crawler) checkBrokenLink(ctx context.Context, sem chan struct{}, ticker *time.Ticker, link Link) (BrokenLink, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, link.URL, nil)
	if err != nil {
		return BrokenLink{URL: link.URL, Error: err.Error()}, true
	}
	response, err := c.doRequest(ctx, sem, ticker, req)
	if err != nil {
		return BrokenLink{URL: link.URL, Error: err.Error()}, true
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= http.StatusBadRequest {
		return BrokenLink{URL: link.URL, StatusCode: response.StatusCode, Error: http.StatusText(response.StatusCode)}, true
	}
	return BrokenLink{}, false
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

func (c *Crawler) fetchAsset(ctx context.Context, sem chan struct{}, ticker *time.Ticker, assetLink AssetLink) Asset {
	asset := Asset{
		URL:  assetLink.URL,
		Type: assetLink.Type,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, asset.URL, nil)
	if err != nil {
		asset.Error = err.Error()
		return asset
	}

	response, err := c.doRequest(ctx, sem, ticker, req)
	if err != nil {
		asset.Error = err.Error()
		return asset
	}
	defer func() { _ = response.Body.Close() }()

	contentLength := response.Header.Get("Content-Length")
	if contentLength == "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
		if err != nil {
			asset.Error = err.Error()
			return asset
		}

		response, err := c.doRequest(ctx, sem, ticker, req)
		if err != nil {
			asset.Error = err.Error()
			return asset
		}
		defer func() { _ = response.Body.Close() }()
		asset.StatusCode = response.StatusCode
		if response.StatusCode >= 400 {
			asset.Error = response.Status
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			asset.SizeBytes = 0
			asset.Error = err.Error()
		} else {
			asset.SizeBytes = int64(len(body))
		}
	} else {
		asset.StatusCode = response.StatusCode
		if response.StatusCode >= 400 {
			asset.Error = response.Status
		}
		asset.SizeBytes = response.ContentLength
	}
	return asset
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

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
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

	initialDomain, err := getDomain(opts.URL)
	if err != nil {
		return []byte{}, err
	}

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

		page, newLinks, err := c.crawl(ctx, sem, ticker, opts.Depth, link)

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

		resp, err := t.Next.RoundTrip(req)

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
