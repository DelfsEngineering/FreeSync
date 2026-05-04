.PHONY: test build test-container

test:
	go test ./...

build:
	go build -o freesync ./cmd/freesync

test-container:
	bash ./scripts/smoke-container.sh
