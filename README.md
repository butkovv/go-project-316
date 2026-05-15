### Hexlet tests and linter status:
[![Actions Status](https://github.com/butkovv/go-project-316/actions/workflows/hexlet-check.yml/badge.svg)](https://github.com/butkovv/go-project-316/actions)

## Crawl Depth

The `depth` setting controls how many link transitions the crawler may follow from the starting URL inside the source domain.

- The starting page always has `depth = 0`.
- Links found on the starting page have `depth = 1`.
- Links found on those pages have `depth = 2`, and so on.
- When the next page depth would exceed the configured limit, the crawler does not add that page to the crawl queue.

Examples:

- `--depth 0` analyzes only the starting page.
- `--depth 1` analyzes the starting page and internal pages linked directly from it.
- `--depth 2` also analyzes internal pages linked from depth-1 pages.

The value can be changed with the CLI flag:

```bash
./bin/hexlet-go-crawler --depth 2 https://example.com
```

Only pages inside the original domain are crawled. Links to external domains are not added to `pages`, but they may still be checked and reported as `broken_links` when unavailable.
