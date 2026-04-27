.PHONY: build install test vet clean test-e2e

BINARY := velocity
INSTALL_DIR ?= $(HOME)/.local/bin

# Version comes from internal/version/VERSION via //go:embed; no
# -ldflags wiring needed. The -s -w pair below strips the debug
# symbol table for size — unrelated to versioning.
build:
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/velocity

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
