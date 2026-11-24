.PHONY: all build build-fips vet test bench demos clean generate run install

# Go parameters (strict pure-Go, zero CGO)
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOINSTALL=$(GOCMD) install

# Reproducible release metadata
VERSION    ?= 1.0.0
EDITION    ?= community
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)

LDFLAGS     := -s -w -X main.Version=$(VERSION) -X main.Edition=$(EDITION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)
BUILD_FLAGS := -trimpath -buildvcs=false -ldflags "$(LDFLAGS)"

# Target binary
BINARY_NAME=vraxter
BINARY_PATH=bin/$(BINARY_NAME)
MAIN_DIR=./cmd/vraxter

all: vet test build

build:
	@echo "Building Vraxter $(VERSION) ($(EDITION), CGO_ENABLED=0, reproducible)..."
	CGO_ENABLED=0 $(GOBUILD) $(BUILD_FLAGS) -o $(BINARY_PATH) $(MAIN_DIR)

build-fips:
	@echo "Building Vraxter $(VERSION) with Go Native FIPS 140-3 module (GOFIPS140=v1.0.0, CGO_ENABLED=0)..."
	CGO_ENABLED=0 GOFIPS140=v1.0.0 $(GOBUILD) $(BUILD_FLAGS) -o bin/$(BINARY_NAME)-fips $(MAIN_DIR)

vet:
	@echo "Running static analysis (go vet)..."
	CGO_ENABLED=0 $(GOVET) ./...

test:
	@echo "Running full pure-Go test suite..."
	CGO_ENABLED=0 $(GOTEST) -count=1 ./...

bench:
	@echo "Running 7-scenario critical benchmark suite..."
	CGO_ENABLED=0 $(GOTEST) -v -count=1 ./tests/benchmarks/...

demos:
	@echo "Running dual-use offline E2E demos..."
	CGO_ENABLED=0 $(GOTEST) -v -count=1 ./demos/...

clean:
	@echo "Cleaning build artifacts..."
	$(GOCLEAN)
	rm -rf bin/
	rm -f coverage.out

generate:
	@echo "Generating Protocol Buffers..."
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/v1/agent.proto

run: build
	./$(BINARY_PATH)

install:
	@echo "Installing Vraxter $(VERSION) to GOPATH/bin..."
	CGO_ENABLED=0 $(GOINSTALL) $(BUILD_FLAGS) $(MAIN_DIR)
