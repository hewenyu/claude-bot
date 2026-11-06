BINARY  := claude-bot
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"

.PHONY: all build install test lint fmt clean web web-check desktop dmg

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

## web: rebuild the gateway page into internal/webui/static (commit the result)
web:
	cd web && npm ci && npm run build

## web-check: fail if the committed page is not what web/ builds
web-check: web
	git diff --exit-code -- internal/webui/static
	@test -z "$$(git status --porcelain -- internal/webui/static)" || (echo "internal/webui/static has untracked files: run make web and commit" && exit 1)

## desktop: run the desktop app (the gateway's page in a window) with bin/claude-bot
desktop: build
	cd desktop && npm ci && npm run typecheck && npm start

## dmg: build desktop/dist/claude-bot-<version>-arm64.dmg (Apple Silicon Mac; not notarized)
dmg: web
	mkdir -p desktop/dist
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath $(LDFLAGS) -o desktop/dist/claude-bot ./cmd/$(BINARY)
	cd desktop && npm ci && npm run typecheck && scripts/dmg.sh "$(patsubst v%,%,$(VERSION))" dist/claude-bot

clean:
	rm -rf bin/
