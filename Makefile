# tracker dev workflow. Requires: go (and docker for the container targets).
BINARY ?= tracker
PLATFORM ?= linux/arm64

.PHONY: build run test test-go test-js vet fmt docker-up docker-down image

## build: compile the server binary
build:
	go build -o bin/$(BINARY) ./cmd/server

## run: run locally on :8080 (serves ./index.html, stores blobs in ./data)
run:
	PORT=8080 DATA_DIR=./data STATIC_DIR=. go run ./cmd/server

## test: run the full suite (Go server + JS sync/merge)
test: test-go test-js

## test-go: run the Go server test suite
test-go:
	go test ./...

## test-js: run the JS sync/merge tests (needs node)
test-js:
	node --test tests/*.test.mjs

## vet: static analysis
vet:
	go vet ./...

## fmt: gofmt
fmt:
	gofmt -w .

## docker-up: build and start the container (needs .env, see README)
docker-up:
	docker compose up --build -d

## docker-down: stop and remove the container (data volume persists)
docker-down:
	docker compose down

## image: build and push to a private registry, e.g. make image REGISTRY=registry.lab.heiz.cc
image:
	@test -n "$(REGISTRY)" || (echo "usage: make image REGISTRY=registry.lab.heiz.cc"; exit 1)
	docker build --platform $(PLATFORM) -t $(REGISTRY)/tracker:latest .
	docker push $(REGISTRY)/tracker:latest
