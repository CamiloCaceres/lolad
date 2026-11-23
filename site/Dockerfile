# Static landing page for lolad. No build step: Caddy serves this folder as-is.
FROM caddy:2-alpine
COPY Caddyfile /etc/caddy/Caddyfile
COPY . /srv
EXPOSE 80
