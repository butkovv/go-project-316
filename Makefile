build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler

lint:
	golangci-lint run ./...

.PHONY: test
test:
	go mod tidy
	go test -v ./... -race

run:
	./bin/hexlet-go-crawler $(URL)
