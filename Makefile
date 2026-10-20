.DEFAULT_GOAL := run

GO ?= go

.PHONY: generate run test

generate:
	$(GO) generate .

run: generate
	$(GO) run ./cmd/example

test: generate
	$(GO) vet ./...
	$(GO) test -race -count=1 ./...
