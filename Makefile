BIN := ./bin/uniche

.PHONY: build run dev lint

build: lint
	@which gotestsum > /dev/null 2>&1 || go install gotest.tools/gotestsum@latest
	@which ogen > /dev/null 2>&1 || go install github.com/ogen-go/ogen/cmd/ogen@latest
	@which templ > /dev/null 2>&1 || go install github.com/a-h/templ/cmd/templ@latest
	templ generate
	go generate ./...
	goimports -w .
	go fix ./...
	gotestsum ./...
	go build -o $(BIN) ./cmd/server

lint:
	@which golangci-lint > /dev/null 2>&1 || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	golangci-lint run --enable unused,staticcheck,dupl --disable errcheck ./...

run:
	go run ./cmd/server

dev: build run
