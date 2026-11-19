VERSION ?= $(shell git describe --tags --always --dirty 2>nul || echo dev)
VERSION := $(subst v,,$(VERSION))
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build build-debug test

build:
	wails build -clean -trimpath -ldflags "$(LDFLAGS)"

build-debug:
	wails build -clean -trimpath -windowsconsole -ldflags "$(LDFLAGS)"

test:
	go test ./... -race -count=1 -cover