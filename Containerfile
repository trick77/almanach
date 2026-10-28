# Built with the repo root as context; the Go module is backend/. The prompt
# is compiled into the binary.
FROM golang:1.27-alpine AS builder
WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# ship release passes VERSION.
ARG VERSION=dev
# Static binary, no cgo: the image needs no runtime besides the CA bundle.
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/trick77/ship/kit/version.Version=${VERSION}" \
    -o /out/almanach ./cmd/almanach

# No shell, no package manager; CA bundle included. Runs as nonroot (65532).
FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app
COPY --from=builder /out/almanach /almanach
EXPOSE 8080
# The binary probes its own /healthz (SERVER_HOST/SERVER_PORT as serve binds).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/almanach", "-healthcheck"]
ENTRYPOINT ["/almanach"]
CMD ["serve"]
