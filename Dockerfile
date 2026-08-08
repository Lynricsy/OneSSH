FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN if [ -f package.json ]; then npm ci; fi
COPY web/ ./
RUN if [ -f package.json ]; then npm run build; else mkdir -p dist && printf '<!doctype html><title>OneSSH</title>' > dist/index.html; fi

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/onessh ./cmd/onessh

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/onessh /usr/local/bin/onessh
VOLUME ["/data"]
EXPOSE 8866
ENTRYPOINT ["onessh"]
