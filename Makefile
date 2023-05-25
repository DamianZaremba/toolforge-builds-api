SWAGGER=docker run --rm -it --user $(shell id -u):$(shell id -g) -e GOPATH=${GOPATH}:/go -v ${PWD}:${PWD} -w ${PWD} quay.io/goswagger/swagger
SHELL := /bin/bash

.PHONY: run image gen-api gen-cli

gen-api:
	$(SWAGGER) generate server -t gen -P models.Principal -f ./swagger/swagger_v1.yaml --exclude-main -A toolforge-builds
	go mod tidy

gen-api-with-main:
	$(SWAGGER) generate server -t gen -P models.Principal -f ./swagger/swagger_v1.yaml -A toolforge-builds
	go mod tidy

gen-cli:
	$(SWAGGER) generate cli -f swagger/swagger_v1.yaml --cli-app-name toolforge-build
	go mod tidy

build-api:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -a -installsuffix cgo -ldflags="-w -s" -o builds-api ./cmd/builds-api

image:
	bash -c "source <(minikube docker-env) || : && docker build --target image -f .pipeline/blubber.yaml . -t toolforge-builds-api:dev"

kind_load:
	bash -c "hash kind 2>/dev/null && kind load docker-image docker.io/library/toolforge-builds-api:dev --name toolforge || :"

rollout:
	kubectl rollout restart -n builds-api deployment builds-api

build-and-deploy-local: image kind_load rollout
	./deploy.sh local
