.PHONY: build build-debug test

build:
	wails build -clean -trimpath -ldflags "-s -w"

build-debug:
	wails build -clean -trimpath -windowsconsole

test:
	go test ./... -race -count=1 -cover