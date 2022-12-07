SHELL = /bin/bash -o pipefail

UNAME_S := $(shell uname -s)

ARCH := $(shell arch)

OS := $(shell test -f /etc/os-release && cat /etc/os-release | grep '^NAME' | sed -e 's/NAME="\(.*\)"/\1/g')

all: clean os-deps dep-tools deps test build

apt-packages:
	sudo apt-get update
	sudo apt install --yes gcc-arm-linux-gnueabihf g++-arm-linux-gnueabihf gcc-aarch64-linux-gnu g++-aarch64-linux-gnu

brew-packages:

alpine-packages:
	apk update
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
	go install github.com/google/wire/cmd/wire@v0.5.0
ifneq ($(OS),Alpine Linux)
	go install github.com/golang-migrate/migrate/v4/cmd/migrate@v4.15.2
	go get github.com/vektra/mockery/v2/.../
endif

build-docker-image:
	docker build . --tag=newscred/webhook-broker:latest

build:
	@echo "Architecture: $(ARCH)"
	go build -mod=readonly -o ./dist/linux/$(ARCH)/webhook-broker
	cp ./dist/linux/$(ARCH)/webhook-broker ./
	$(eval build_archs := \
		$(ARCH)\
	)
ifeq ($(OS),Ubuntu)
	$(eval build_archs := \
		x86_64\
		arm64\
		arm-v7\
	)
	env CC=aarch64-linux-gnu-gcc CXX=aarch64-linux-gnu-g++ CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -mod=readonly -o ./dist/linux/arm64/webhook-broker
	env CC=arm-linux-gnueabihf-gcc CXX=arm-linux-gnueabihf-g++ CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 go build -mod=readonly -o ./dist/linux/arm-v7/webhook-broker
endif
ifndef APP_VERSION
	@echo "Version: $(shell git log --pretty=format:'%h' -n 1)"
	$(eval this_version := $(shell git log --pretty=format:'%h' -n 1))
endif
ifdef APP_VERSION
	@echo "Version (Ref): $(APP_VERSION)"
	$(eval this_version := $(APP_VERSION))
endif
	@- $(foreach X,$(build_archs), \
	tar cjvf ./dist/webhook-broker-$(this_version)_$(X).tar.bz2 -C ./dist/linux/$(X)/ .;\
	)

time-test:
	time go test -timeout 30s -mod=readonly ./... -count=1

ci-test:
	go test -timeout 30s -mod=readonly -v ./... -short

test:
	go test -timeout 30s -mod=readonly ./...

install: build
	go install -mod=readonly

setup-docker:
	cp ./webhook-broker.cfg.template ./dist/webhook-broker.cfg

clean:
	mkdir -p ./dist/
	-rm -v ./testdatadir/*.cfg
	-rm -vrf ./dist/*
	-rm -v webhook-broker

itest-down:
	- docker-compose -f docker-compose.integration-test.yaml down

itest: itest-down
	docker-compose -f docker-compose.integration-test.yaml build
	docker-compose -f docker-compose.integration-test.yaml up tester
