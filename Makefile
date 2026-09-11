SHELL := /bin/sh

VERSION := $(shell tr -d '\n' < VERSION)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOFLAGS ?= -trimpath -buildvcs=false
LDFLAGS := -s -w \
	-X control-center/internal/buildinfo.Version=$(VERSION) \
	-X control-center/internal/buildinfo.Commit=$(COMMIT) \
	-X control-center/internal/buildinfo.BuildTime=$(BUILD_TIME)

.PHONY: all build test test-race vet fmt-check check ci clean image

all: check build

build:
	mkdir -p bin
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o bin/control-center ./cmd/control-center

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l cmd internal migrations)" || { gofmt -d cmd internal migrations; exit 1; }

check: fmt-check vet test-race

ci: check build

image:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t control-center:$(VERSION) .

clean:
	rm -rf bin dist coverage.out coverage.html
