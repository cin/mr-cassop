FROM golang:1.26-trixie AS builder

WORKDIR /workspace

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/

ARG VERSION=undefined
ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GO111MODULE=on \
    go build \
    -ldflags "-X main.Version=$VERSION" \
    -a \
    -o bin/mr-cassop main.go

FROM debian:trixie-slim

WORKDIR /

RUN apt-get update && \
    apt-get install -y ca-certificates && \
    update-ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --gid 901 mr-cassop && \
    useradd --uid 901 --gid 901 --home-dir /home/mr-cassop --create-home mr-cassop

COPY --from=builder /workspace/bin/mr-cassop .
USER mr-cassop

ENTRYPOINT ["/mr-cassop"]
