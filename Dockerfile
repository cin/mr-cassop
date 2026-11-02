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
    -o bin/mr-cassop main.go

# The operator writes client TLS material and webhook certs under $HOME. Distroless sets no HOME and
# has no home directory for its nonroot user, so it is created here and HOME is set explicitly.
RUN mkdir -p /home/mr-cassop

# The binary is static (CGO_ENABLED=0) and runs no shell commands, so it needs no libc, shell or
# package manager. distroless/static ships the CA certificates. The image runs as its own nonroot
# user (UID/GID 65532). The chart overrides that with 901 in the pod securityContext
# (mr-cassop/templates/deployment.yaml) and mounts an emptyDir at $HOME, which works for any UID.
FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /

COPY --from=builder --chown=65532:65532 /home/mr-cassop /home/mr-cassop
COPY --from=builder /workspace/bin/mr-cassop .

ENV HOME=/home/mr-cassop

ENTRYPOINT ["/mr-cassop"]
