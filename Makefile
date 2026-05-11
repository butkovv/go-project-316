build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler

lint:
	golangci-lint run ./...

test:
	go mod tidy
	go test -v ./...

run:
	./bin/hexlet-go-crawler $(URL)
