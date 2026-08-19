BINARY  := mybox
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/overworks/mybox-cli/internal/cli.Version=$(VERSION)

.PHONY: build test lint install clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/mybox

test:
	go test -race -cover ./...

lint:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "gofmt를 실행하세요:"; echo "$$out"; exit 1; fi
	go vet ./...

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/mybox

clean:
	rm -f $(BINARY) coverage.out
	rm -rf dist
