FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN if [ -f package.json ]; then npm ci; fi
COPY web/ ./
RUN if [ -f package.json ]; then npm run build; else mkdir -p dist && printf '<!doctype html><title>OneSSH</title>' > dist/index.html; fi

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/onessh ./cmd/onessh

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/Lynricsy/OneSSH" \
      org.opencontainers.image.description="Centralized SSH gateway with WebUI and MCP"
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/onessh /usr/local/bin/onessh
VOLUME ["/data"]
EXPOSE 8866
ENTRYPOINT ["onessh"]
