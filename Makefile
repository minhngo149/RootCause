.PHONY: build test lint run

build:
	go build -o bin/rootcause ./cmd/rootcause

test:
	go test ./...

lint:
	go vet ./...

run: build
	./bin/rootcause
