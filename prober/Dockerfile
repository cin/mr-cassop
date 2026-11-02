FROM golang:1.26-trixie AS builder

LABEL org.opencontainers.image.source="https://github.com/cin/mr-cassop" \
      description="This docker image is used for the prober Go application"

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=undefined
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GO111MODULE=on \
    go build \
    -ldflags "-X main.Version=$VERSION" \
    -o prober .

# The binary is static (CGO_ENABLED=0), writes no files and runs no shell commands, so it needs no
# libc, shell or package manager. distroless/static ships the CA certificates. The image runs as
# its own nonroot user (UID/GID 65532), which is in the image's passwd and group files so the
# primary group resolves too. A numeric USER missing from those files would run with GID 0.
FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /

COPY --from=builder /build/prober/prober .

ENTRYPOINT ["/prober"]
