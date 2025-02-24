.PHONY: all build clean test run

BINARY_NAME=bookmark-parser
GO=go

all: build

build:
	$(GO) build -o $(BINARY_NAME)

clean:
	$(GO) clean
	rm -f $(BINARY_NAME)
	if [ -f "data/bookmarks.db" ] ; then rm -f data/bookmarks.db ; fi

test:
	$(GO) test ./...

run:
	$(GO) run main.go

install:
	$(GO) install

fmt:
	$(GO)fmt ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

check: fmt vet lint test