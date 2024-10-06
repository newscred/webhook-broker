FROM golang:1.23.0-alpine3.20

RUN apk update && apk add bash make git

RUN mkdir -p /go/src/github.com/newscred/webhook-broker/integration-test/
WORKDIR /go/src/github.com/newscred/webhook-broker/integration-test/

ADD Makefile .
RUN make os-deps dep-tools

ADD go.mod .
ADD go.sum .
RUN make deps

ADD main.go .

RUN make build
EXPOSE 61500-63000
