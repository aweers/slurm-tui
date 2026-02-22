SHELL := /bin/sh

APP_NAME ?= slurm-tui
DIST_DIR ?= dist
GO ?= go
GOFLAGS ?= -trimpath
LDFLAGS ?= -s -w
GOOS ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)

.PHONY: build test clean linux-amd64 linux-arm64 release

build:
	$(GO) build -o $(APP_NAME) .

test:
	$(GO) test ./...

linux-amd64:
	$(MAKE) release GOOS=linux GOARCH=amd64

linux-arm64:
	$(MAKE) release GOOS=linux GOARCH=arm64

release:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP_NAME)-$(GOOS)-$(GOARCH) .

clean:
	rm -rf $(DIST_DIR)
