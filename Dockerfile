FROM golang:1.23.0-alpine3.20 AS build-env

RUN apk update && apk add bash make git

RUN mkdir -p /go/src/github.com/newscred/webhook-broker/
WORKDIR /go/src/github.com/newscred/webhook-broker/

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
ADD utils ./utils
ADD prune ./prune

RUN make build
RUN make test

FROM alpine:3.20
RUN apk update && apk add curl
WORKDIR /
COPY --from=build-env /go/src/github.com/newscred/webhook-broker/webhook-broker /webhook-broker
COPY --from=build-env /go/src/github.com/newscred/webhook-broker/migration /migration
CMD [ "webhook-broker", "-migrate", "/migration/sqls/" ]
