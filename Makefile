.PHONY: build install test vet clean test-e2e

BINARY := velocity
INSTALL_DIR ?= $(HOME)/.local/bin

# VERSION_TAG defaults to `git describe` (closest tag + dirty marker)
# so local builds report something meaningful. CI / release builds
# override this with the exact release tag.
VERSION_TAG ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/randheer094/velocity/internal/version.Tag=$(VERSION_TAG)

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/velocity

install: build
	mkdir -p $(INSTALL_DIR)
	mv $(BINARY) $(INSTALL_DIR)/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

# Boots the compose Postgres, runs all tests against it, and tears the
# container down on exit. Data under .pgdata/ persists between runs.
test-e2e:
	./scripts/test-db.sh
