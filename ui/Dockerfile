FROM golang:1.26-trixie AS builder

LABEL org.opencontainers.image.source="https://github.com/cin/mr-cassop" \
      description="This docker image is used for the mr-cassop nodetool UI"

WORKDIR /build
COPY go.mod ./
COPY . .

ARG VERSION=undefined
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GO111MODULE=on \
    go build \
    -ldflags "-X main.Version=$VERSION" \
    -a \
    -o ui .

FROM debian:trixie-slim

WORKDIR /

RUN apt-get update && \
    apt-get upgrade -y && \
    apt-get install -y ca-certificates && \
    update-ca-certificates && \
    rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 901 ui && \
    useradd --uid 901 --gid 901 --create-home ui

USER ui

COPY --from=builder /build/ui .

EXPOSE 8080

CMD ["./ui"]
