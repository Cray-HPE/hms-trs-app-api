NAME ?= hms-trs-app-api
VERSION ?= $(shell cat .version)

all: image unittest coverage integration

image:
	docker build --pull ${DOCKER_ARGS} --tag '${NAME}:${VERSION}' .

unittest:
	./runUnitTest.sh

coverage:
	./runCoverage.sh

integration:
	./runIntegration.sh

