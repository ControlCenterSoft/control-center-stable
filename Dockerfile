# syntax=docker/dockerfile:1.7
FROM golang:1.23-alpine3.21 AS build

ARG VERSION=0.6.0
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -buildvcs=false \
    -ldflags="-s -w \
      -X control-center/internal/buildinfo.Version=${VERSION} \
      -X control-center/internal/buildinfo.Commit=${COMMIT} \
      -X control-center/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/control-center ./cmd/control-center

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 control-center \
    && adduser -S -D -H -u 10001 -G control-center control-center

COPY --from=build /out/control-center /usr/local/bin/control-center

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/control-center"]
