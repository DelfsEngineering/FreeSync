.PHONY: test build

test:
	go test ./...

build:
	go build -o freesync ./cmd/freesync
