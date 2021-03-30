FROM golang:1.16.2-alpine3.13 AS build-env

RUN apk update && apk add bash make git

RUN mkdir -p /go/src/github.com/imyousuf/webhook-broker/
WORKDIR /go/src/github.com/imyousuf/webhook-broker/

RUN mkdir -p ./dist/
ADD Makefile .
RUN make os-deps dep-tools

ADD go.mod .
ADD go.sum .
RUN make deps

ADD main.go .
ADD main_test.go .
ADD wire.go .
ADD wire_gen.go .
ADD testdatadir ./testdatadir
ADD config ./config
ADD controllers ./controllers
ADD migration ./migration
ADD storage ./storage
ADD dispatcher ./dispatcher

RUN make build
RUN make test

FROM alpine:3.13
RUN apk update && apk add curl
WORKDIR /
COPY --from=build-env /go/src/github.com/imyousuf/webhook-broker/webhook-broker /webhook-broker
COPY --from=build-env /go/src/github.com/imyousuf/webhook-broker/migration /migration
CMD [ "webhook-broker", "-migrate", "/migration/sqls/" ]
