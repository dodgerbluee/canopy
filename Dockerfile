FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-build
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY server/ ./
ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/ ./cmd/api ./cmd/worker ./cmd/migrate ./cmd/admin ./cmd/canopy-mcp

FROM --platform=$BUILDPLATFORM node:24 AS web-build
WORKDIR /src/contracts
COPY contracts/package.json contracts/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ ./
COPY contracts/generated/ /src/contracts/generated/
RUN npm run build -- --output-path=/out

FROM caddy:2-alpine AS caddy
FROM docker:cli AS dockercli

# Codec packages match docker/media.Dockerfile. Tests keep that image;
# this deployment image carries the same tools so the existing sandbox
# can run them by replacing CMD. There is intentionally no ENTRYPOINT.
FROM ubuntu:24.04@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      libvips-tools \
      libheif1 \
      libheif-plugin-libde265 \
      tesseract-ocr \
      poppler-utils \
      ca-certificates \
      tzdata \
      wget \
 && wget -q -O /usr/share/keyrings/postgresql.asc https://www.postgresql.org/media/keys/ACCC4CF8.asc \
 && echo 'deb [signed-by=/usr/share/keyrings/postgresql.asc] https://apt.postgresql.org/pub/repos/apt noble-pgdg main' > /etc/apt/sources.list.d/postgresql.list \
 && apt-get update && apt-get install -y --no-install-recommends postgresql-client-17 \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 10001 canopy \
 && useradd --uid 10001 --gid canopy --home-dir /var/lib/canopy --no-create-home --shell /usr/sbin/nologin canopy \
 && install -d -o 10001 -g 10001 /var/lib/canopy /work

COPY --from=go-build /out/ /usr/local/bin/
COPY --from=caddy /usr/bin/caddy /usr/local/bin/caddy
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
COPY docker/canopy-run /usr/local/bin/canopy-run
COPY --from=web-build /out/browser/ /srv/
COPY docker/Caddyfile.web /etc/caddy/Caddyfile
RUN chmod 755 /usr/local/bin/canopy-run /usr/local/bin/caddy /usr/local/bin/docker \
 && caddy version \
 && docker --version \
 && setpriv --reuid=10001 --regid=10001 --groups 10001,0 true

ENV CANOPY_ENV=dev \
    CANOPY_SITE=http://:80 \
    CANOPY_STORAGE_ROOT=/var/lib/canopy \
    CANOPY_TRUST_PROXY=1 \
    CANOPY_MCP_ENABLED=true \
    TMPDIR=/srv/canopy/work \
    XDG_CONFIG_HOME=/config \
    XDG_DATA_HOME=/data \
    CANOPY_API_UPSTREAM=127.0.0.1:8080
WORKDIR /work
EXPOSE 80 443
HEALTHCHECK --start-period=30s --interval=10s --timeout=5s --retries=18 CMD test -f /run/canopy-ready && wget -q -O /dev/null http://127.0.0.1/readyz || exit 1
CMD ["/usr/local/bin/canopy-run"]
