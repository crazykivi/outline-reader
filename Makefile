VERSION ?= $(shell git describe --tags --always --dirty 2>nul || echo dev)
VERSION := $(subst v,,$(VERSION))
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build build-debug generate-icon test

generate-icon:
	go run ./tools/icon-gen -input build/appicon.png -output build/windows/icon.ico

build: generate-icon
	wails build -clean -trimpath -ldflags "$(LDFLAGS)"

build-debug: generate-icon
	wails build -clean -trimpath -windowsconsole -ldflags "$(LDFLAGS)"

test:
	go test ./... -race -count=1 -cover