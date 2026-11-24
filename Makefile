VERSION := $(shell cat VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)
export PATH := $(HOME)/sdk/go1.27.4/bin:$(PATH)

.PHONY: build test plugin-test plugin-archive clean

build:
	go build -ldflags '$(LDFLAGS)' -o qdnder ./cmd/qdnder

test:
	go test ./...

plugin-test:
	python3 plugin/qdnder/deploy/test_plugin.py

plugin-archive:
	mkdir -p dist
	tar czf dist/qdnder-plugin.tar.gz -C plugin qdnder
	cd plugin && zip -qr ../dist/qdnder-plugin.zip qdnder
	@ls -lh dist/qdnder-plugin.*

clean:
	rm -rf qdnder dist
