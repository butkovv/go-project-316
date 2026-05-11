package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Options struct {
	URL         string
	Depth       int64
	Retries     int64
	Delay       time.Duration
	Timeout     time.Duration
	UserAgent   string
	Concurrency int64
	IndentJSON  int64
	HTTPClient  *http.Client
}

type Page struct {
	URL        string `json:"url"`
	Depth      int64  `json:"depth"`
	HTTPStatus int64  `json:"http_status"`
	Status     string `json:"status"`
	Error      error  `json:"error"`
}

type Report struct {
	RootURL     string    `json:"root_url"`
	Depth       int64     `json:"depth"`
	GeneratedAt time.Time `json:"generated_at"`
	Pages       []Page    `json:"pages"`
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	report := Report{
		RootURL:     opts.URL,
		Depth:       1,
		GeneratedAt: time.Now(),
		Pages:       []Page{},
	}

	response, err := opts.HTTPClient.Get(opts.URL)
	if err != nil {
		return []byte{}, err
	}
	defer response.Body.Close()

	page := Page{
		URL:        opts.URL,
		Depth:      0,
		HTTPStatus: int64(response.StatusCode),
		Status:     response.Status,
		Error:      err,
	}

	report.Pages = append(report.Pages, page)

	json, err := json.Marshal(report)
	if err != nil {
		return []byte{}, err
	}
	return json, nil
}
