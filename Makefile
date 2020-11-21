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
	go mod vendor
	go generate -mod=readonly

dep-tools:
	go get github.com/google/wire/cmd/wire

build:
	go build -mod=readonly
	cp ./webhook-broker ./dist/
	@echo "Version: $(shell git log --pretty=format:'%h' -n 1)"
	(cd dist && tar cjvf webhook-broker-$(shell git log --pretty=format:'%h' -n 1).tar.bz2 ./webhook-broker)

test:
	go test -mod=readonly ./...

install: build-web
	go install -mod=readonly

setup-docker:
	cp ./webhook-broker.cfg.template ./dist/webhook-broker.cfg

clean:
	mkdir -p ./dist/
	-rm -vrf ./dist/*
	-rm -v webhook-broker

