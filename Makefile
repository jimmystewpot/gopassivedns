#!/usr/bin/make
SHELL  := /bin/bash

export PATH = /usr/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/bin:/sbin:/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/build/bin

BINPATH := bin
GO_DIR := src/github.com/jimmystewpot/gopassivedns/
DOCKER_IMAGE := golang:1.15-buster
TOOL := gopassivedns

get-golang:
	docker pull ${DOCKER_IMAGE}

.PHONY: clean
clean:
	@echo $(shell docker images -qa -f 'dangling=true'|egrep '[a-z0-9]+' && docker rmi $(shell docker images -qa -f 'dangling=true'))

#
# build the software
#
build: get-golang
	@docker run \
		--rm \
		-v $(CURDIR):/build/$(GO_DIR) \
		--workdir /build/$(GO_DIR) \
		-e GOPATH=/build \
		-e PATH=$(PATH) \
		-t ${DOCKER_IMAGE} \
		make build-targets

build-targets: apt test gopassivedns

apt:
	@echo ""
	@echo "***** installing libpcap-dev from apt *****"
	apt update && apt -y install libpcap-dev

gopassivedns:
	@echo ""
	@echo "***** Building gopassivedns binary *****"
	GOOS=linux GOARCH=amd64 \
	go build -ldflags="-s -w" -o $(BINPATH)/$(TOOL) ./cmd/$(TOOL)
	@echo ""

# install used when building locally.
install:
	install -g 0 -o 0 -m 0755 -D $(BINPATH)/$(TOOL) /opt/$(TOOL)/$(TOOL)

#
# Testing and Benchmarking Targets
#
test: benchmark
	@echo ""
	@echo "***** running gopassivedns tests *****"
	GOOS=linux GOARCH=amd64 \
	go test -a -v ./cmd/$(TOOL)
	@echo ""

benchmark:
	@echo ""
	@echo "***** running gopassivedns benchmarks *****"
	go test -bench=. -benchmem  ./cmd/$(TOOL)

benchmark-with-profile:
	@echo ""
	@echo "***** running gopassivedns benchmarks with profiling *****"
	go test -bench=. -benchmem -memprofile profilemem.out -cpuprofile profilecpu.out  ./cmd/$(TOOL)