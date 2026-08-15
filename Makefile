.PHONY: test build vet fmt dist snapshot

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -o git-declutter -ldflags "$(LDFLAGS)" .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Cross-compile locally (no GitHub Release). Needs Go.
dist:
	mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/git-declutter_darwin_arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/git-declutter_darwin_amd64 .
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/git-declutter_linux_arm64 .
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/git-declutter_linux_amd64 .
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/git-declutter_windows_arm64.exe .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/git-declutter_windows_amd64.exe .

# Dry-run a GitHub Release locally (needs goreleaser). Does not publish.
snapshot:
	goreleaser release --snapshot --clean

