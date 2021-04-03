ifneq ($(SEMVER),)
	_TAG :=$(SEMVER)
else
	_TAG :=1.0.3
endif

_APP_NAME := ocspd
_REG_URL := 913152793797.dkr.ecr.us-west-2.amazonaws.com
_REG_PREFIX := utility
_ECR_REGION := us-west-2

usage:
	@echo "Usage:"
	@echo "make local_build {SEMVER=}"
	@echo "make docker_build {SEMVER=}"
	@echo "make docker_debug {SEMVER=}"
	@echo "make push {SEMVER=}"
	@echo "make run {SEMVER=}"

local_build:  ## To get a local copy of the executable
	export GOPATH=$$PWD; go install gocsp-responder/main  

docker_build:  ## Build the executable inside a container
	@docker build '--network=host' -t ${_REG_PREFIX}/${_APP_NAME}:latest \
		-t ${_REG_PREFIX}/${_APP_NAME}:${_TAG} \
		-t ${_REG_URL}/${_REG_PREFIX}/${_APP_NAME}:${_TAG} .

docker_debug:  ## Build the container
	@docker build '--network=host' -t ${_REG_PREFIX}/${_APP_NAME}:debug -f Dockerfile.debug .

run: docker_debug  ## Run the container
	@docker run --entrypoint=sh -it --rm ${_REG_PREFIX}/${_APP_NAME}:debug

login:
	@echo "logging into ECR"
	@aws ecr get-login-password --region ${_ECR_REGION} | docker login --username AWS --password-stdin ${_REG_URL}

push: docker_build login
	@docker push ${_REG_URL}/${_REG_PREFIX}/${_APP_NAME}:${_TAG}

