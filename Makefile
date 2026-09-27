# feedme — self-hosted full-text feed generator
export GOTOOLCHAIN ?= go1.25.11

BIN     := bin/feedme
VERSION := 0.1.0

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
