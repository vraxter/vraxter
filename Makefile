.PHONY: build build-fips test clean generate run install

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOINSTALL=$(GOCMD) install

# Target binary
BINARY_NAME=vraxter
BINARY_PATH=bin/$(BINARY_NAME)
MAIN_DIR=./cmd/vraxter

all: test build

build: 
	@echo "Building Vraxter..."
	$(GOBUILD) -o $(BINARY_PATH) $(MAIN_DIR)

build-fips:
	@echo "Building Vraxter with FIPS 140-2 compliance (BoringCrypto)..."
	GOEXPERIMENT=boringcrypto $(GOBUILD) -tags fips -o bin/vraxter-fips $(MAIN_DIR)

test: 
	@echo "Running tests..."
	$(GOTEST) -v ./...

clean: 
	@echo "Cleaning up..."
	$(GOCLEAN)
	rm -rf bin/
	rm -f coverage.out

generate:
	@echo "Generating Protocol Buffers..."
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/v1/agent.proto

run:
	@echo "Building and running Vraxter..."
	$(GOBUILD) -o $(BINARY_PATH) $(MAIN_DIR)
	./$(BINARY_PATH)

install:
	@echo "Installing Vraxter to GOPATH/bin..."
	$(GOINSTALL) $(MAIN_DIR)
