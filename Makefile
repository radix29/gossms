BINARY   := gossms
MODULE   := github.com/radix29/gossms
CMD      := ./cmd/gossms
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION) -s -w"

.PHONY: all build run tidy check-gosmo lint test clean fmt release

all: build

## Verify the gosmo sibling checkout exists (required by the go.mod
## `replace github.com/radix29/gosmo => ../gosmo` directive).
check-gosmo:
	@test -d ../gosmo || { \
		echo "error: ../gosmo not found."; \
		echo "  go.mod replaces github.com/radix29/gosmo with a local sibling checkout."; \
		echo "  Clone it alongside this repo: git clone https://github.com/radix29/gosmo.git ../gosmo"; \
		exit 1; \
	}

## Download dependencies
tidy: check-gosmo
	go mod tidy

## Build for the current platform
build: tidy
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

## Run directly
run:
	go run $(CMD)

## Format all Go source files
fmt:
	gofmt -w ./...

## Run tests
test:
	go test ./...

## Clean build artifacts
clean:
	rm -f $(BINARY) $(BINARY).exe $(BINARY)-linux $(BINARY)-darwin $(BINARY)-windows.exe
	rm -f gossms.log

## Cross-compile release binaries
release: tidy
	GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux       $(CMD)
	GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-darwin      $(CMD)
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-darwin-arm64 $(CMD)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-windows.exe $(CMD)
	@echo "Release binaries built:"
	@ls -lh $(BINARY)-linux $(BINARY)-darwin $(BINARY)-darwin-arm64 $(BINARY)-windows.exe
