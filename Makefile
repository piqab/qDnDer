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
	find plugin -name __pycache__ -type d -exec rm -rf {} + 2>/dev/null || true
	rm -f dist/qdnder-plugin.tar.gz dist/qdnder-plugin.zip
	tar czf dist/qdnder-plugin.tar.gz --exclude '__pycache__' -C plugin qdnder
	cd plugin && zip -qr ../dist/qdnder-plugin.zip qdnder -x '*__pycache__*'
	@ls -lh dist/qdnder-plugin.*

clean:
	rm -rf qdnder dist
