SHELL := /bin/bash
API_CLASS_NAME := toolforge-builds
PROJECT := builds-api
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

IMAGE_NAME=tools-harbor.wmcloud.org/toolforge/$(PROJECT):dev

ifdef DOCKER
	DOCKER=podman
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
	DOCKER=docker
	BUILD_IMAGE=docker \
		build \
			--target image \
			-f .pipeline/blubber.yaml \
			. \
			-t $(IMAGE_NAME)
	KEEP_ID=
endif

SWAGGER=$(DOCKER) run $(KEEP_ID) \
	--rm \
	-it \
	--user $(shell id -u):$(shell id -g) \
	-e GOPATH=${GOPATH}:/go \
	-v ${PWD}:${PWD}:rw \
	-w ${PWD} \
	quay.io/goswagger/swagger


.PHONY: run image gen-api gen-cli get-api-with-main build-api podman-image kind_load rollout build-and-deploy-local check_requirements

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

gen-api: check_requirements
	$(SWAGGER) generate server -t gen -P models.Principal -f ./swagger/swagger_v1.yaml --exclude-main -A $(API_CLASS_NAME)
	go mod tidy

build-api:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -a -installsuffix cgo -ldflags="-w -s" -o $(PROJECT) ./cmd/$(PROJECT)

image: check_requirements
ifdef MINIKUBE
	$(BUILD_IMAGE) >/tmp/image.tar
	minikube image load /tmp/image.tar
else
ifdef PODMAN
	$(BUILD_IMAGE) | podman load
else
	$(BUILD_IMAGE)
endif
	kind load docker-image $(IMAGE_NAME) --name toolforge
endif

rollout: check_requirements
	bash -c "if kubectl get namespace $(PROJECT) >/dev/null 2>&1; then kubectl rollout restart -n $(PROJECT) deployment $(PROJECT); else :; fi"

build-and-deploy-local: image rollout
	./deploy.sh local
