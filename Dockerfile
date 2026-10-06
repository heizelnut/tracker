# Multi-stage build. No CGO and no dependencies: the binary is fully static.

# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tracker ./cmd/server

# ---- run ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 tracker

WORKDIR /app
COPY --from=build /out/tracker /app/tracker
COPY index.html /app/index.html

# Blobs live on the /data volume; ownership matters for the first mount.
RUN mkdir -p /data && chown -R tracker:tracker /data /app
USER tracker

EXPOSE 8080
VOLUME /data
ENV PORT=8080 DATA_DIR=/data STATIC_DIR=/app

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/tracker"]
