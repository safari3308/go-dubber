.PHONY: test build build-web build-all clean

BINARY_NAME := go-dubber
GO := $(shell which go || echo /usr/local/go/bin/go)

test:
	$(GO) test -v -race ./...

build:
	$(GO) build -o bin/$(BINARY_NAME) ./cmd/go-dubber

build-web:
	cd web && npm install && npm run build

build-all: build-web build

clean:
	rm -rf bin/ temp/
