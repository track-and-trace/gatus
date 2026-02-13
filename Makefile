BINARY=gatus

.PHONY: install
install:
	go build -v -o $(BINARY) .

.PHONY: run
run:
	ENVIRONMENT=dev GATUS_CONFIG_PATH=./config.yaml go run main.go

.PHONY: run-binary
run-binary:
	ENVIRONMENT=dev GATUS_CONFIG_PATH=./config.yaml ./$(BINARY)

.PHONY: clean
clean:
	rm $(BINARY)

.PHONY: test
test:
	go test ./... -cover


##########
# Docker #
##########

AWS_ACCOUNT_ID=$(shell aws sts get-caller-identity --profile kvinta.kvinta-dev --query Account --output text)
ECR_IMAGE=$(AWS_ACCOUNT_ID).dkr.ecr.eu-central-1.amazonaws.com/infra/gatus:v5.35.0.1-kv

docker-build:
	docker build -t twinproduction/gatus:latest .

docker-build-kvinta:
	docker buildx build --no-cache --platform linux/amd64,linux/arm64 -t $(ECR_IMAGE) --push .

docker-run:
	docker run -p 8080:8080 --name gatus twinproduction/gatus:latest

docker-build-and-run: docker-build docker-run


#############
# Front end #
#############

frontend-install-dependencies:
	npm --prefix web/app install

frontend-build:
	npm --prefix web/app run build

frontend-run:
	npm --prefix web/app run serve
