FROM golang:1.26-trixie AS builder

LABEL org.opencontainers.image.source="https://github.com/cin/mr-cassop" \
      description="This docker image is used for the mr-cassop nodetool UI"

WORKDIR /build
COPY . .

ARG VERSION=undefined
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GO111MODULE=on \
    go build \
    -ldflags "-X main.Version=$VERSION" \
    -o ui .

# The binary is static (CGO_ENABLED=0), so it needs no libc or shell. The nonroot tag runs as
# UID/GID 65532, which the operator's pod securityContext pins (controllers/ui.go).
FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /

COPY --from=builder /build/ui .

EXPOSE 8080

ENTRYPOINT ["/ui"]
