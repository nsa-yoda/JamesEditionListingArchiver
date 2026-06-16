.PHONY: fmt vet test race build check serve

ARCHIVE_ROOT ?= listings
PORT ?= 8000

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

build:
	go build -o listing-archiver ./cmd/listing-archiver

check: fmt vet test race build

serve:
	python3 -m http.server --directory "$(ARCHIVE_ROOT)" "$(PORT)"
