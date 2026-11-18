VERSION ?= $(shell git describe --tags --always --dirty 2>nul || echo dev)
VERSION := $(subst v,,$(VERSION))
LDFLAGS := -s -w -X main.version=$(VERSION)
TAGS := sqlite_fts5

.PHONY: build build-debug test release

build:
	wails build -clean -trimpath -tags "$(TAGS)" -ldflags "$(LDFLAGS)"

build-debug:
	wails build -clean -trimpath -windowsconsole -tags "$(TAGS)" -ldflags "$(LDFLAGS)"

test:
	go test ./... -tags "$(TAGS)" -race -count=1 -cover