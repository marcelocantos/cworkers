BINARY := cworkers
INSTALL_DIR := /usr/local/bin

.PHONY: build test clean install

build: $(BINARY)

$(BINARY): main.go go.mod
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)

install: build
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
