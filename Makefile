.PHONY: build release run clean test test-unit build-loadtest loadtest loadtest-realistic

BINARY := scaleset-ec2-provider
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

build:
	go build -o dist/$(BINARY) ./cmd/scaleset-ec2-provider

release:
	go build -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" -o dist/$(BINARY) ./cmd/scaleset-ec2-provider

run: build
	./dist/$(BINARY)

clean:
	rm -f dist/*

test:
	go test ./... -v

test-unit:
	go test ./... -short -v

build-loadtest:
	go build -o dist/loadtest ./cmd/loadtest

loadtest: build-loadtest
	./dist/loadtest -jobs=100 -rate=10 -simulation=instant

loadtest-realistic: build-loadtest
	./dist/loadtest -jobs=50 -rate=5 -simulation=realistic -max-duration=15m

