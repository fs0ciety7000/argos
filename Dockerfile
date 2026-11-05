# syntax=docker/dockerfile:1

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24-alpine AS api
WORKDIR /src
COPY go.mod ./
COPY go.su[m] ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/argos ./cmd/argos

# Alpine rather than distroless: Coolify's health check needs wget/curl inside
# the container. Runs as an unprivileged user; /data is pre-owned so a fresh
# named volume inherits the right ownership.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
 && addgroup -S -g 65532 argos \
 && adduser -S -D -H -u 65532 -G argos argos \
 && mkdir -p /data && chown argos:argos /data && chmod 0700 /data
COPY --from=api /out/argos /usr/local/bin/argos
ENV ARGOS_ADDR=:8080 ARGOS_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER argos:argos
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/argos"]
