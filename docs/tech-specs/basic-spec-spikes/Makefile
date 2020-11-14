all: clean dep-tools deps build

os-deps:

deps:
	go mod vendor

dep-tools:
	go get github.com/google/wire/cmd/wire

build:
	go generate -mod=readonly
	go build -mod=readonly

clean:
	-rm -v basic-spec-spikes

