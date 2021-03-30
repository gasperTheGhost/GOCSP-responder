ifneq ($(SEMVER),)
	_TAG :=$(SEMVER)
else
	_TAG :=1.0.1
endif

_APP_NAME := ocspd
_REG_URL := 913152793797.dkr.ecr.us-west-2.amazonaws.com
_REG_PREFIX := utility
_ECR_REGION := us-west-2

usage:
	@echo "Usage:"
	@echo "make build {SEMVER=}"
	@echo "make build_debug {SEMVER=}"
	@echo "make push {SEMVER=}"
	@echo "make run {SEMVER=}"

build:  ## Build the container
	@docker build '--network=host' -t ${_REG_PREFIX}/${_APP_NAME}:latest \
		-t ${_REG_PREFIX}/${_APP_NAME}:${_TAG} \
		-t ${_REG_URL}/${_REG_PREFIX}/${_APP_NAME}:${_TAG} .

build_debug:  ## Build the container
	@docker build '--network=host' -t ${_REG_PREFIX}/${_APP_NAME}:debug -f Dockerfile.debug .

run: build_debug	## Run the container
	@docker run --entrypoint=sh -it --rm ${_REG_PREFIX}/${_APP_NAME}:debug

login:
	@echo "logging into ECR"
	@aws ecr get-login-password --region ${_ECR_REGION} | docker login --username AWS --password-stdin ${_REG_URL}

push: build login
	@docker push ${_REG_URL}/${_REG_PREFIX}/${_APP_NAME}:${_TAG}

