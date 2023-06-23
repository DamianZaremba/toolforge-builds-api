SHELL := /bin/bash
.DEFAULT_GOAL := build-and-deploy-local
# For podman, you need to run buildkit:
# > podman run --rm -d --name buildkitd --privileged moby/buildkit:latest
#
# And install buildctl (https://github.com/moby/buildkit/releases)
ifneq ($(strip $(shell command -v podman 2>/dev/null)), )
PODMAN=$(shell command -v podman)
endif

ifneq ($(strip $(shell command -v docker 2>/dev/null)), )
DOCKER=$(shell command -v docker)
endif

ifneq ($(strip $(shell command -v minikube 2>/dev/null)), )
MINIKUBE=$(shell command -v minikube)
endif

ifneq ($(strip $(shell command -v kind 2>/dev/null)), )
KIND=$(shell command -v kind)
endif

ifneq ($(strip $(shell command -v oapi-codegen 2>/dev/null)), )
OAPI=$(shell command -v oapi-codegen)
endif

PROJECT_SLUG=builds
IMAGE_NAME=tools-harbor.wmcloud.org/toolforge/$(PROJECT_SLUG)-api:dev

ifdef PODMAN
	DOCKER=$(PODMAN)
	BUILD_IMAGE=buildctl \
		--addr=podman-container://buildkitd \
		build \
			--progress=plain \
			--frontend=gateway.v0 \
			--opt source=docker-registry.wikimedia.org/repos/releng/blubber/buildkit:v0.16.0 \
			--local context=. \
			--local dockerfile=. \
			--opt filename=.pipeline/blubber.yaml \
			--opt target=image \
			--output type=docker,name=$(IMAGE_NAME)
	KEEP_ID=--userns=keep-id
else
	BUILD_IMAGE=$(DOCKER) \
		build \
			--target image \
			-f .pipeline/blubber.yaml \
			. \
			-t $(IMAGE_NAME)
	KEEP_ID=
endif

.PHONY: run image gen-api build-api rollout build-and-deploy-local check_requirements

check_requirements:
ifdef PODMAN
	@echo "Using podman ($(PODMAN)) to build the images"
else
ifdef DOCKER
	@echo "Using docker ($(DOCKER)) to build the images"
else
	@echo "You need docker or podman installed"
	exit 1
endif
endif
ifdef MINIKUBE
	@echo "Using minikube ($(MINIKUBE)) to run the application"
else
ifdef KIND
	@echo "Using kind ($(KIND)) to run the application"
else
	@echo "You need minikube or kind installed"
	exit 1
endif
endif
ifndef OAPI
	@echo "You need oapi-codegen installed, see https://github.com/deepmap/oapi-codegen"
	exit 1
endif

gen-api: check_requirements
	$(OAPI) -config openapi/gen_config/api_config.yaml openapi/v1.yaml
	$(OAPI) -config openapi/gen_config/models_config.yaml openapi/v1.yaml
	go mod tidy

build-api:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -a -installsuffix cgo -ldflags="-w -s" -o $(PROJECT_SLUG)-api ./cmd/main.go

image: check_requirements
ifdef MINIKUBE
ifdef PODMAN
# minikube + podman
	$(BUILD_IMAGE) >/tmp/image.tar
	minikube image load /tmp/image.tar
	rm -f /tmp/image.tar
else
# minikube + docker
	bash -c "source <(minikube docker-env) && $(BUILD_IMAGE)"
endif
else
ifdef PODMAN
# kind + podman
	$(BUILD_IMAGE) | podman load
else
# kind + docker
	$(BUILD_IMAGE)
endif
# 	kind with both podman and docker
	kind load docker-image $(IMAGE_NAME) --name toolforge
endif

rollout: check_requirements
	bash -c "if kubectl get namespace $(PROJECT_SLUG)-api >/dev/null 2>&1; then kubectl rollout restart -n $(PROJECT_SLUG)-api deployment $(PROJECT_SLUG)-api; else :; fi"

build-and-deploy-local: image rollout
	./deploy.sh local
