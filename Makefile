.PHONY: all build test lint fmt tidy clean

BINARY := mitsume
BUILD_FLAGS := -trimpath -ldflags=-s -ldflags=-w

all: build

build:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY) ./cmd/mitsume

test:
	go test ./... -race -shuffle=on

lint:
	golangci-lint run

fmt:
	gofumpt -w .

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
