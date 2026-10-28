BINARY  := claude-bot
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"

.PHONY: all build install test lint fmt clean

all: lint test build

## build: build bin/claude-bot
build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)

## install: install claude-bot into GOBIN
install:
	go install $(LDFLAGS) ./cmd/$(BINARY)

## test: run tests with the race detector
test:
	go test -race ./...

## lint: run golangci-lint
lint:
	golangci-lint run ./...

## fmt: format code
fmt:
	golangci-lint fmt ./...

clean:
	rm -rf bin/
