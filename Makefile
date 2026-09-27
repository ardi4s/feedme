# feedme — RSS feed creator
export GOTOOLCHAIN ?= go1.25.11

BIN     := bin/feedme

# The version a local build reports. Releases do not read this: release.yml
# stamps the tag into the binary itself. Reading the tag here means the value
# cannot go stale the way the literal it replaced did, and a checkout with no
# git — a source tarball — falls back to the Dockerfile's own default.
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null)
VERSION     ?= $(patsubst v%,%,$(if $(GIT_VERSION),$(GIT_VERSION),docker))

.PHONY: all build test vet fmt clean run install tidy

all: vet test build

build:
	@mkdir -p bin
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) ./cmd/feedme

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/feedme

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

clean:
	rm -rf bin

run: build
	$(BIN)
