SHELL := bash

.PHONY : build-static

IMAGE_TAG = garm-build

USER_ID=$(shell (docker --version | grep -q podman) && echo "0" || shell id -u)
USER_GROUP=$(shell (docker --version | grep -q podman) && echo "0" || shell id -g)

build-static:
	@echo Building garm
	docker build --tag $(IMAGE_TAG) .
	docker run --rm -e USER_ID=$(USER_ID) -e USER_GROUP=$(USER_GROUP) -v $(PWD):/build/garm $(IMAGE_TAG) /build-static.sh
	@echo Binaries are available in $(PWD)/bin