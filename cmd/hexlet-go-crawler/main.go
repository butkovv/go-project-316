package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"code/crawler"

	cli "github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:  "hexlet-go-crawler",
		Usage: "analyze a website structure",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "depth",
				Value: 10,
				Usage: "crawl depth",
			},
			&cli.IntFlag{
				Name:  "retries",
				Value: 1,
				Usage: "number of retries for failed requests",
			},
			&cli.DurationFlag{
				Name:  "delay",
				Value: 0 * time.Second,
				Usage: "delay between requests",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: 15 * time.Second,
				Usage: "per-request timeout",
			},
			&cli.IntFlag{
				Name:  "rps",
				Value: 0,
				Usage: "limit requests per second (overrides delay)",
			},
			&cli.StringFlag{
				Name:  "user-agent",
				Usage: "custom user agent",
			},
			&cli.IntFlag{
				Name:  "workers",
				Value: 4,
				Usage: "number of concurrent workers",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			url := cmd.Args().Get(0)
			if url == "" {
				return fmt.Errorf("hexlet-go-crawler: не указан url")
			}
			client := &http.Client{
				Timeout: cmd.Duration("timeout"),
			}
			options := crawler.Options{
				URL:         url,
				Depth:       cmd.Int("depth"),
				Retries:     cmd.Int("retries"),
				Delay:       cmd.Duration("retries"),
				Timeout:     cmd.Duration("timeout"),
				UserAgent:   cmd.String("user-agent"),
				Concurrency: cmd.Int("workers"),
				IndentJSON:  0,
				HTTPClient:  client,
			}
			res, err := crawler.Analyze(ctx, options)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(res)
			if err != nil {
				return err
			}
			return nil
		},
	}

	err := cmd.Run(context.Background(), os.Args)

	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}
