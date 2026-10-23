FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914 AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN if [ -f package.json ]; then npm ci; fi
COPY web/ ./
RUN if [ -f package.json ]; then npm run build; else mkdir -p dist && printf '<!doctype html><title>OneSSH</title>' > dist/index.html; fi

FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go run ./internal/searchx/helper/genassets
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/onessh ./cmd/onessh

FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f
LABEL org.opencontainers.image.source="https://github.com/Lynricsy/OneSSH" \
      org.opencontainers.image.description="Centralized SSH gateway with WebUI and MCP"
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/onessh /usr/local/bin/onessh
VOLUME ["/data"]
EXPOSE 8866
ENTRYPOINT ["onessh"]
