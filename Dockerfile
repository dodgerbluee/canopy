# syntax=docker/dockerfile:1
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

# API and worker deliberately share one release artifact, including matching
# migration/admin binaries. Docker CLI is used only by the isolated codec seam.
FROM alpine:3.22 AS server
RUN apk add --no-cache ca-certificates tzdata docker-cli postgresql17-client \
    && addgroup -g 10001 canopy && adduser -D -H -u 10001 -G canopy canopy
COPY --from=go-build /out/ /usr/local/bin/
USER 10001:10001
WORKDIR /var/lib/canopy
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]

FROM caddy:2-alpine AS web
COPY --from=web-build /out/browser/ /srv/
COPY docker/Caddyfile.web /etc/caddy/Caddyfile
