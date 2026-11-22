# Imagen de movistar-nube-webdav: Movistar Cloud se usa como la web oficial, con un
# Chromium sin pantalla que guarda la sesión, y se sirve por WebDAV.
FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# La versión sale de internal/config/env.go.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /movistar-proxy ./cmd/server

# Chromium completo de Debian (no headless-shell): mismo motor y huella que un Chrome normal.
FROM debian:trixie-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends fuse3 chromium fonts-dejavu-core fonts-liberation ca-certificates tzdata gosu && \
    rm -rf /var/lib/apt/lists/* && \
    ln -s "$(command -v gosu)" /usr/local/bin/su-exec && \
    useradd -m -u 10001 app && \
    mkdir -p /app/tokens && \
    chown -R app:app /app

WORKDIR /app

COPY --from=builder /movistar-proxy /app/movistar-proxy
# rclone interno: sirve por WebDAV los archivos ya descifrados (crypt + chunker sobre
# el propio proxy) con la clave guardada en el proxy.
COPY --from=rclone/rclone:1.76.0 /usr/local/bin/rclone /usr/local/bin/rclone
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# Chromium escribe caché y configuración en HOME; con read_only se apunta a /tmp (tmpfs).
ENV CHROME_PATH=/usr/bin/chromium \
    CHROME_NO_SANDBOX=true \
    HOME=/tmp \
    XDG_CONFIG_HOME=/tmp/.config \
    XDG_CACHE_HOME=/tmp/.cache

VOLUME ["/app/tokens"]

EXPOSE 5000 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/app/movistar-proxy", "-healthcheck"]

ENTRYPOINT ["/app/docker-entrypoint.sh"]
