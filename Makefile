.PHONY: build install test vet clean

BINARY := velocity
INSTALL_DIR ?= $(HOME)/.local/bin

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
