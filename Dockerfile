ARG DOCKER_PROXY_REGISTRY=""
FROM ${DOCKER_PROXY_REGISTRY}golang:1.18 as builder

WORKDIR /workspace

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/

ARG VERSION=undefined

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on \
    go build \
    -ldflags "-X main.Version=$VERSION" \
    -a \
    -o bin/mr-cassop main.go

ARG DOCKER_PROXY_REGISTRY=""
FROM ${DOCKER_PROXY_REGISTRY}debian:buster-slim

WORKDIR /


RUN apt-get update && \
    apt-get install -y ca-certificates && \
    update-ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    addgroup --gid 901 mr-cassop && \
    adduser --uid 901 --gid 901 --home /home/mr-cassop mr-cassop

COPY --from=builder /workspace/bin/mr-cassop .
USER mr-cassop

ENTRYPOINT ["/mr-cassop"]
