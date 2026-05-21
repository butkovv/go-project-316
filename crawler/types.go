package crawler

import (
	"net/http"
	"sync"
	"time"
)

// Options configures a crawler analysis run.
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

// Link represents a discovered page URL and its crawl depth.
type Link struct {
	URL   string
	Depth int
}

// AssetType identifies a discovered asset category.
type AssetType string

// AssetLink represents an asset URL found in page markup before fetching metadata.
type AssetLink struct {
	URL  string
	Type AssetType
}

const (
	// AssetTypeImage identifies image assets.
	AssetTypeImage AssetType = "image"
	// AssetTypeScript identifies JavaScript assets.
	AssetTypeScript AssetType = "script"
	// AssetTypeStyle identifies stylesheet assets.
	AssetTypeStyle AssetType = "style"
	// AssetTypeOther identifies assets that do not fit a known type.
	AssetTypeOther AssetType = "other"
)

// Asset describes a page asset and its HTTP metadata.
type Asset struct {
	URL        string    `json:"url"`
	Type       AssetType `json:"type"`
	StatusCode int       `json:"status_code"`
	SizeBytes  int64     `json:"size_bytes"`
	Error      string    `json:"error,omitempty"`
}

// AssetsCache stores fetched asset metadata by URL.
type AssetsCache struct {
	cacheMu  sync.RWMutex
	data     map[string]Asset
	keyLocks sync.Map
}

// SEO contains extracted SEO-related page metadata.
type SEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

// BrokenLink describes a link that failed validation.
type BrokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error"`
}

// Page describes one crawled page in the report.
type Page struct {
	URL          string       `json:"url"`
	Depth        int          `json:"depth"`
	HTTPStatus   int          `json:"http_status"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
	SEO          SEO          `json:"seo"`
	BrokenLinks  []BrokenLink `json:"broken_links"`
	Assets       []Asset      `json:"assets"`
	DiscoveredAt time.Time    `json:"discovered_at"`
}

// Report is the top-level crawler analysis output.
type Report struct {
	RootURL     string    `json:"root_url"`
	Depth       int       `json:"depth"`
	GeneratedAt time.Time `json:"generated_at"`
	Pages       []Page    `json:"pages"`
}

// Crawler holds mutable crawl state for one analysis run.
type Crawler struct {
	visited     map[string]bool
	assetsCache *AssetsCache
	httpclient  *http.Client
	mu          sync.Mutex
}

// RetryTransport retries temporary transport failures and retryable HTTP statuses.
type RetryTransport struct {
	Next       http.RoundTripper
	MaxRetries int
	BaseDelay  time.Duration
}
