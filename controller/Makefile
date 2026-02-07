VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
IMAGE   ?= gastown-controller
LDFLAGS  = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test lint docker-build clean

build:
	go build -ldflags="$(LDFLAGS)" -o bin/controller ./cmd/controller/

test:
	go test -v -race ./...

lint:
	golangci-lint run ./...

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):$(VERSION) .

clean:
	rm -rf bin/
