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

# Not distroless any more: the clones are git's, and git needs a userland.
# Debian's git and CA bundle, nothing else; no ssh (https remotes only). The
# passwd entry is for git, which looks its own uid up. /app/repos (the
# clones) and /data (the database) exist in the image so an empty named
# volume mounted there takes their ownership, and the uid-65532 process can
# write to it on first start; a PVC needs fsGroup 65532 or an init chown for
# the same reason. 65532, never 1000: 1000 is a Debian host's first user,
# so a chown for the container would hand files to a real account. The
# upgrade takes Debian fixes the base tag has not caught up with.
FROM debian:13-slim
RUN apt-get update \
 && apt-get upgrade -y \
 && apt-get install -y --no-install-recommends ca-certificates git \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 65532 almanach \
 && useradd --uid 65532 --gid 65532 --no-create-home --home-dir /tmp --shell /usr/sbin/nologin almanach \
 && mkdir -p /app/repos /data && chown 65532:65532 /app /app/repos /data
WORKDIR /app
COPY --from=builder /out/almanach /almanach
USER 65532:65532
EXPOSE 8080
# The binary probes its own /healthz (SERVER_HOST/SERVER_PORT as serve binds).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/almanach", "-healthcheck"]
ENTRYPOINT ["/almanach"]
CMD ["serve"]
