NAME ?= hms-trs-app-api
VERSION ?= $(shell cat .version)

all: image unittest coverage snyk integration

image:
	docker build --pull ${DOCKER_ARGS} --tag '${NAME}:${VERSION}' .

unittest:
	./runUnitTest.sh

coverage:
	./runCoverage.sh

snyk:
	./runSnyk.sh

integration:
	./runIntegration.sh

buildbase:
	docker build -t cray/hms-trs-app-api-build-base -f Dockerfile.build-base .

