#SHELL := /bin/bash
BINARY_DIR := build
SO := $(BINARY_DIR)/mosq_pg_auth.so
BCRYPT := $(BINARY_DIR)/bcryptgen

GOFLAGS :=
CGO_ENABLED := 1

.PHONY: all build bcryptgen clean docker-build docker-run mod

all: build bcryptgen

mod:
	go mod tidy

build: mod
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=$(CGO_ENABLED) go build -buildmode=c-shared -o $(SO) ./

bcryptgen:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_DIR)/bcryptgen ./cmd/bcryptgen

clean:
	rm -rf $(BINARY_DIR)

# Build a runnable Mosquitto image with the plugin baked in
docker-build:
	docker build -f Dockerfile-debian -t mosquitto:latest .

# Quick run; assumes a postgres reachable per mosquitto.conf DSN
docker-run:
	docker run --rm -it \
	  --network host \
	  -v $(PWD)/mosquitto.conf:/mosquitto/config/mosquitto.conf:ro \
	  mosquitto:latest

local-run: build
	mosquitto -c ./mosquitto.conf -v