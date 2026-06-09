SHELL := /bin/bash

ifneq ($(strip $(shell command -v oapi-codegen 2>/dev/null)), )
OAPI=$(shell command -v oapi-codegen)
endif

OAPI_CODEGEN_VERSION=v2.1.0
PROJECT_SLUG=builds

.PHONY: help gen-api build-api check_genapi_requirements install-oapi-codegen unit-tests static-tests test

help:
	@echo "Make targets:"
	@echo "============="
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "%-20s\t%s\n", $$1, $$2}'


check_genapi_requirements: ## Check if required tools are installed
ifndef OAPI
	@echo "You need oapi-codegen installed. Run 'make install-oapi-codegen' to install it"
	exit 1
endif

install-oapi-codegen: ## Install oapi-codegen
	go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

gen-api: check_genapi_requirements ## Generate API code from OpenAPI specification
	@if [ "$$(oapi-codegen -version 2>/dev/null | grep $(OAPI_CODEGEN_VERSION))" = "" ]; then \
		echo "Warning: Your oapi-codegen version does not match $(OAPI_CODEGEN_VERSION). Please run 'make install-oapi-codegen'"; \
		exit 1; \
	fi
	$(OAPI) -config openapi/gen_config/api_config.yaml openapi/openapi.yaml
	$(OAPI) -config openapi/gen_config/models_config.yaml openapi/openapi.yaml
	go mod tidy

build-api: ## Build the API
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -a -installsuffix cgo -ldflags="-w -s" -o $(PROJECT_SLUG)-api ./cmd/main.go

unit-tests: ## Run unit tests
	@echo "Running unit tests..."
	@go test ./...

static-tests: ## Run static tests
	@echo "Running static tests..."
	@pre-commit run -a

test: static-tests unit-tests ## Run unit and static tests
