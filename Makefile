all: clean os-deps dep-tools deps test build

os-deps:

deps:
	go mod vendor

dep-tools:
	go get github.com/google/wire/cmd/wire

build-web:

build: build-web
	go generate -mod=readonly
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

