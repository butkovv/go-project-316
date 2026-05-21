package crawler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

func (c *Crawler) crawl(ctx context.Context, sem chan struct{}, ticker *time.Ticker, maxDepth int, link Link) (Page, []Link, error) {
	req, err := newGetRequest(ctx, link.URL)
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

	pageLinks, assetLinks := c.collectLinksAndAssets(doc, link.Depth+1, link.URL)

	for _, l := range pageLinks {
		brokenLink, ok := c.checkBrokenLink(ctx, sem, ticker, l)
		if ok {
			page.BrokenLinks = append(page.BrokenLinks, brokenLink)
		}
	}

	sort.SliceStable(assetLinks, func(i, j int) bool {
		return assetTypeRank(assetLinks[i].Type) < assetTypeRank(assetLinks[j].Type)
	})
	for _, assetLink := range assetLinks {
		asset := c.assetsCache.GetOrFetchAsset(ctx, sem, ticker, assetLink, c.fetchAsset)
		page.Assets = append(page.Assets, asset)
	}

	newLinks := []Link{}
	if link.Depth < maxDepth {
		newLinks = pageLinks
	}
	return page, newLinks, nil
}

func (c *Crawler) collectLinksAndAssets(n *html.Node, depth int, baseUrl string) ([]Link, []AssetLink) {
	links := []Link{}
	assetLinks := []AssetLink{}

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "a":
				if href := strings.TrimSpace(getAttr(node, "href")); href != "" {
					if resolved, ok := resolveURL(baseUrl, href); ok {
						links = append(links, Link{URL: canonicalURL(resolved), Depth: depth})
					}
				}
			case "img":
				if src := strings.TrimSpace(getAttr(node, "src")); src != "" {
					if resolved, ok := resolveURL(baseUrl, src); ok {
						assetLinks = append(assetLinks, AssetLink{URL: resolved, Type: AssetTypeImage})
					}
				}
			case "script":
				if src := strings.TrimSpace(getAttr(node, "src")); src != "" {
					if resolved, ok := resolveURL(baseUrl, src); ok {
						assetLinks = append(assetLinks, AssetLink{URL: resolved, Type: AssetTypeScript})
					}
				}
			case "link":
				if getAttr(node, "rel") == "stylesheet" {
					if href := strings.TrimSpace(getAttr(node, "href")); href != "" {
						if resolved, ok := resolveURL(baseUrl, href); ok {
							assetLinks = append(assetLinks, AssetLink{URL: resolved, Type: AssetTypeStyle})
						}
					}
				}
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}

	walk(n)
	return links, assetLinks
}

func (c *Crawler) markVisited(URL string) bool {
	URL = canonicalURL(URL)
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

		if tokenType == html.StartTagToken && token.Data == "title" && !seo.HasTitle {
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

func (ac *AssetsCache) getMutex(key string) *sync.Mutex {
	newMu := &sync.Mutex{}
	actual, _ := ac.keyLocks.LoadOrStore(key, newMu)

	return actual.(*sync.Mutex)
}

// GetOrFetchAsset returns cached asset metadata or fetches and stores it once.
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

func assetTypeRank(t AssetType) int {
	switch t {
	case AssetTypeImage:
		return 0
	case AssetTypeScript:
		return 1
	case AssetTypeStyle:
		return 2
	default:
		return 3
	}
}
