BINARY := cworkers
INSTALL_DIR := /usr/local/bin
VERSION ?= dev

.PHONY: build test clean install

build: $(BINARY)

$(BINARY): main.go go.mod
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)

install: build
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
