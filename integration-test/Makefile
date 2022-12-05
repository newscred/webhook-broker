SHELL = /bin/bash -o pipefail

UNAME_S := $(shell uname -s)
OS := $(shell test -f /etc/os-release && cat /etc/os-release | grep '^NAME' | sed -e 's/NAME="\(.*\)"/\1/g')

all: clean os-deps dep-tools deps test build

apt-packages:

brew-packages:

alpine-packages:

os-deps:
ifeq ($(UNAME_S),Linux)
ifeq ($(OS),Ubuntu)
os-deps: apt-packages
endif
ifeq ($(OS),LinuxMint)
os-deps: apt-packages
endif
ifeq ($(OS),Alpine Linux)
os-deps: alpine-packages
endif
endif
ifeq ($(UNAME_S),Darwin)
os-deps: brew-packages
endif

deps:
ifeq ($(OS),Alpine Linux)
	go mod download
endif
ifneq ($(OS),Alpine Linux)
	go mod vendor
endif

generate:

dep-tools:

build-docker-iamge:
	docker build . --tag=newscred/webhook-broker-integration-test:latest

build:
	go build -mod=readonly

install:
	go install -mod=readonly

clean:
	-rm -v integration-test

run: build
	./integration-test