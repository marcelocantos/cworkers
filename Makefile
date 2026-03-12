BINARY := cworkers
INSTALL_DIR := /usr/local/bin
VERSION ?= dev

.PHONY: build test clean install dashboard

build: $(BINARY)

dashboard/dist/index.html: $(wildcard dashboard/src/*.js dashboard/src/**/*.svelte dashboard/src/**/*.js dashboard/src/**/*.css) dashboard/package.json dashboard/vite.config.js
	cd dashboard && npm run build

$(BINARY): main.go go.mod dashboard/dist/index.html
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

dashboard:
	cd dashboard && npm run build

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf dashboard/dist

install: build
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
