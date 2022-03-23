
_APP_NAME := ocspd
_REG_ID := 678565182846
_REG_URL := 678565182846.dkr.ecr.us-west-2.amazonaws.com
_REG_PREFIX := utility
_ECR_REGION := us-west-2
_NEXUS_HELM_URL := https://nexus.black.powerdata.west.com/repository/helm/
_S3_HELM := intrado-pd-dev-helm
_S3_GOCD := intrado-pd-dev-gocd

# Dynamic variables
GET_COMMIT_ID = $(eval COMMIT_ID=`git rev-parse --short HEAD`)
CHECK_ECR = $(eval ECR = $(shell cat checkecr 2>/dev/null))
CHECK_HELM = $(eval HELM = $(shell cat checkhelm 2>/dev/null))
CHECK_DEPLOY = $(eval DEPLOY = $(shell cat checkdeploy 2>/dev/null))

NAMESPACE ?= dev
VALUES_FILE ?= black-dev-$(_APP_NAME)-values.yaml

usage:
	@echo "Usage:"
	@echo
	@echo "Troubleshooting/local targets:"
	@echo "    make clean"
	@echo "    make local-build"
	@echo "    make docker-debug"
	@echo "    make run"
	@echo
	@echo "To execute the docker stage: (tailored for the GoCD alpine-dood or apline-golang agent - does a docker build and push)"
	@echo "    make docker VERSION=version"
	@echo
	@echo "To execute the helm stage: (tailored for the GoCD alpine-helm agent - does a helm package and push to Nexus)"
	@echo "    make helm VERSION=version NEXUS_CREDS=nexus_creds"
	@echo
	@echo "To execute the deploy stage: (tailored for the GoCD alpine-helm agent - does a helm upgrade/install to dev namespace)"
	@echo "    make deploy VERSION=version {VALUES_FILE=values_file} {NAMESPACE=namespace}"
	@echo
	@echo "NOTES:"
	@echo "    The NEXUS_CREDS should contain the credentials to login to the nexus host, e.g.:"
	@echo "    NEXUS_CREDS=username:password"
	@echo
	@echo "UTILITY TARGETS:"
	@echo "    The target: pull-values-file will pull the values file specified by the make argument VALUES_FILES from AWS S3"
	@echo "    and leaves the file on the filesystem as: values.yaml"
	@echo
	@echo "    make pull-values-file VALUES_FILE=black-dev-$(_APP_NAME)-values.yaml"
	@echo
	@echo "    The target: push-values-file will push the values.yaml file to S3 and store it with the name specified by"
	@echo "    the make argument VALUES_FILES."
	@echo
	@echo "    make push-values-file VALUES_FILE=black-dev-$(_APP_NAME)-values.yaml"
	@echo
	@echo "    Values filenames are typically named in the format: [cluster]-[namespace]-[service]-values.yaml"
	@echo "    e.g.: black-dev-$(_APP_NAME)-values.yaml"
	@echo
	@echo "    DO NOT add/commit the values.yaml file to Git repo."


clean:
	rm -f docker-build helm-package
	rm -f update-helm-repo pull-kubeconfig pull-values-file
	rm -f semver checkecr checkhelm checkdeploy
	rm -rf kubconfig bin


semver:
ifneq ($(VERSION),)
	$(GET_COMMIT_ID)
	get_semver -v $(VERSION) -n $(_APP_NAME) -i $(shell echo $(COMMIT_ID)) > semver
else
ifneq ($(SEMVER),)
	echo "$(SEMVER)" > semver
else
	$(error Must set either VERSION or SEMVER)
endif
endif

##########
# Local targets
##########

local-build:  ## To get a local copy of the executable
	export GOPATH=$$PWD; export GO111MODULE=off; go install gocsp-responder/main

docker-debug:  ## Build the container
	docker build '--network=host' -t ${_REG_PREFIX}/${_APP_NAME}:debug -f Dockerfile.debug .

run: docker-debug  ## Run the container starting a shell
	docker run -it --rm --entrypoint sh $(_REG_PREFIX)/$(_APP_NAME):debug

##########
# Docker targets
#
# Docker image is only built when it does not already exist in ECR.
##########

get-ecr-version: semver
	aws ecr --region $(_ECR_REGION) list-images --registry-id $(_REG_ID) --repository-name $(_REG_PREFIX)/$(_APP_NAME) --query 'imageIds[*].imageTag' | grep -F "\"$(shell cat semver)\"" > checkecr || echo "ignore error if not exist"

checkecr: get-ecr-version
	$(CHECK_ECR)
	$(if $(ECR), \
		touch docker-build, \
		rm -f docker-build)

docker-build: semver  ## Build the executable inside a container
	docker build '--network=host' -t $(_REG_PREFIX)/$(_APP_NAME):latest \
		-t $(_REG_PREFIX)/$(_APP_NAME):$(shell cat semver) \
		-t $(_REG_URL)/$(_REG_PREFIX)/$(_APP_NAME):$(shell cat semver) .

docker-login:
	$(info logging into ECR)
	aws ecr get-login-password --region $(_ECR_REGION) | docker login --username AWS --password-stdin $(_REG_URL)

docker: checkecr docker-build docker-login
	$(CHECK_ECR)
	$(if $(ECR), $(info $(_APP_NAME)-$(shell cat semver) already exists in ECR), docker push $(_REG_URL)/$(_REG_PREFIX)/$(_APP_NAME):$(shell cat semver))

##########
# Helm chart targets
#
# Helm chart is only built when the chart version does not exist in Nexus.
##########

find-chart: semver
ifeq ($(NEXUS_CREDS),)
	$(error Thou shalt set NEXUS_CREDS)
endif
	# Determine if this chart version already exists in Nexus
	curl --silent -o /dev/null -Iw '%{http_code}' -u $(NEXUS_CREDS) $(_NEXUS_HELM_URL)$(_APP_NAME)-$(shell cat semver).tgz | grep '200' > checkhelm || echo "ignore any error from curl"

checkhelm: find-chart
	$(CHECK_HELM)
	$(if $(HELM), \
		touch helm-package, \
		rm -f helm-package)

helm-package:
	helm lint ./helm-chart
	helm package ./helm-chart --version $(shell cat semver) --app-version $(shell cat semver) -d dist

helm: checkhelm helm-package
	$(CHECK_HELM)
	$(if $(HELM), $(info $(_APP_NAME)-$(shell cat semver) already exists in helm repo), \
		curl -i -u $(NEXUS_CREDS) $(_NEXUS_HELM_URL) --upload-file ./dist/$(_APP_NAME)-$(shell cat semver).tgz)

##########
# Helm deploy targets
#
# Helm chart is only deployed when the chart version is not currently deployed.
##########

get-deploy-version: semver
	# Get the currently deployed version
	helm ls -n $(NAMESPACE) -f $(_APP_NAME) -o json  | jq '.[].app_version' | tr -d '"' | grep -F "$(shell cat semver)" > checkdeploy || echo "ignore error if not exist"

checkdeploy: get-deploy-version
	$(CHECK_DEPLOY)
	$(if $(DEPLOY), \
		touch update-helm-repo pull-kubeconfig pull-values-file, \
		rm -f update-helm-repo pull-kubeconfig pull-values-file)

pull-kubeconfig:
	aws sts get-caller-identity
	aws s3 sync s3://$(_S3_GOCD)/kubeconfig kubeconfig
	@chmod 0400 kubeconfig/*
	@kubectl version

update-helm-repo:
	helm repo update

deploy: checkdeploy update-helm-repo pull-kubeconfig pull-values-file
	$(CHECK_DEPLOY)
	$(if $(DEPLOY), \
		$(info chart: $(_APP_NAME), version: $(HELM) already exists in K8S), \
		helm upgrade -i --version $(shell cat semver) --values values.yaml $(_APP_NAME) $(_REG_PREFIX)/$(_APP_NAME) -n $(NAMESPACE))

##########
# Helm values file utility targets
##########
pull-values-file:
	$(info pulling $(VALUES_FILE))
	aws sts get-caller-identity
	aws s3api get-object --bucket $(_S3_HELM) --key values/$(VALUES_FILE) values.yaml

push-values-file:
	$(info pushing $(VALUES_FILE))
	aws sts get-caller-identity
	aws s3api put-object --server-side-encryption AES256 --acl bucket-owner-full-control --bucket $(_S3_HELM) --key values/$(VALUES_FILE) --body values.yaml

