# Built with the repo root as context: the image also builds ui/, beside
# backend/. Hence the backend/ prefix on Go paths. The prompt is compiled
# into the binary.

# The wiki SPA, built first: the Go stage embeds its output, so it has to
# exist on disk before `go build` reads it.
FROM node:26-alpine AS ui
WORKDIR /ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.27-alpine AS builder
WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# vite writes into backend/web/dist (ui/vite.config.ts), which in this stage
# is /build/web/dist. Copied AFTER the source, or the COPY above would put
# the tracked .gitkeep back over it.
COPY --from=ui /backend/web/dist ./web/dist
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
