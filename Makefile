SHELL = /bin/bash -o pipefail

UNAME_S := $(shell uname -s)
OS := $(shell test -f /etc/os-release && cat /etc/os-release | grep '^NAME' | sed -e 's/NAME="\(.*\)"/\1/g')

all: clean os-deps dep-tools deps test build

apt-packages:

brew-packages:

alpine-packages:
	apk add --no-cache gcc musl-dev curl

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
	go generate -mod=readonly
	(cd storage && go generate -mod=readonly)
	mockery --all --dir "./config/" --output "./config/mocks"
	mockery --all --dir "./storage/" --output "./storage/mocks"
	mockery --all --dir "./dispatcher/" --output "./dispatcher/mocks"
	mockery --name ChannelRepository --structname "MockChannelRepository" --dir "./storage/" --output "./storage" --testonly --outpkg "storage"
	mockery --name ProducerRepository --structname "MockProducerRepository" --dir "./storage/" --output "./storage" --testonly --outpkg "storage"

dep-tools:
	go get github.com/google/wire/cmd/wire
ifneq ($(OS),Alpine Linux)
	go get github.com/golang-migrate/migrate/v4/cmd/migrate
	go get github.com/vektra/mockery/v2/.../
endif

build-docker-iamge:
	docker build . --tag=imyousuf/webhook-broker:latest

build:
	go build -mod=readonly
	cp ./webhook-broker ./dist/
	@echo "Version: $(shell git log --pretty=format:'%h' -n 1)"
	(cd dist && tar cjvf webhook-broker-$(shell git log --pretty=format:'%h' -n 1).tar.bz2 ./webhook-broker)

time-test:
	time go test -timeout 30s -mod=readonly ./... -count=1

ci-test:
	go test -timeout 30s -mod=readonly -v ./... -short

test:
	go test -mod=readonly ./...

install: build
	go install -mod=readonly

setup-docker:
	cp ./webhook-broker.cfg.template ./dist/webhook-broker.cfg

clean:
	mkdir -p ./dist/
	-rm -vrf ./dist/*
	-rm -v webhook-broker

itest-down:
	- docker-compose -f docker-compose.integration-test.yaml down

itest: itest-down
	docker-compose -f docker-compose.integration-test.yaml build
	docker-compose -f docker-compose.integration-test.yaml run tester
