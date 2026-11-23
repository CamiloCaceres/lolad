# lolad-hub container (Coolify, or any Docker host). Build from the repo root:
#
#   docker build -f deploy/hub/Dockerfile -t lolad-hub .
#
# Coolify: Build Pack "Dockerfile", Base Directory "/", Dockerfile Location
# "/deploy/hub/Dockerfile", Ports Exposes 8095, a persistent volume on
# /var/lib/lolad-hub, and the env vars below. See deploy/hub/README.md.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lolad-hub ./cmd/lolad-hub

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 lolad-hub \
 && mkdir -p /var/lib/lolad-hub && chown lolad-hub:lolad-hub /var/lib/lolad-hub
COPY --from=build /out/lolad-hub /usr/local/bin/lolad-hub
USER lolad-hub
VOLUME /var/lib/lolad-hub

# LOLAD_HUB_PUBLIC_URL must be set at deploy time (https://<your hub domain>).
# The proxy in front (Traefik under Coolify) reaches the container over a
# private Docker network, so its X-Forwarded-For is trusted from those ranges.
# Only the proxy can reach the container: do not publish the port publicly.
ENV LOLAD_HUB_LISTEN=0.0.0.0:8095 \
    LOLAD_HUB_DB=/var/lib/lolad-hub/hub.db \
    LOLAD_HUB_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7
EXPOSE 8095
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD wget -q -O /dev/null http://127.0.0.1:8095/v1/hub/info || exit 1
STOPSIGNAL SIGTERM
ENTRYPOINT ["lolad-hub"]
CMD ["serve"]
