SWAGGER=docker run --rm -it --user $(shell id -u):$(shell id -g) -e GOPATH=${GOPATH}:/go -v ${PWD}:${PWD} -w ${PWD} quay.io/goswagger/swagger

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
	go build ./cmd/builds-api

image:
	docker build . --tag=toolforge-builds-api:dev