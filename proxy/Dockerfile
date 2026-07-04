# Build a static, dependency-free proxy binary and ship it on a tiny base.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY src/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sandbox-proxy .

FROM alpine:3.20
# ca-certificates gives Go the system root store it needs to VERIFY upstream
# TLS (github.com, npm, ...); su-exec lets the entrypoint drop from root to the
# proxy user after fixing volume perms.
RUN apk add --no-cache ca-certificates su-exec && adduser -D -u 10001 proxy
COPY --from=build /sandbox-proxy /usr/local/bin/sandbox-proxy
COPY config.json /etc/sandbox-proxy/config.json
COPY entrypoint.sh /usr/local/bin/proxy-entrypoint.sh
RUN chmod +x /usr/local/bin/proxy-entrypoint.sh
# Start as root so the entrypoint can chown /ca; it then drops to 'proxy'.
ENV PROXY_LISTEN=:3128 \
    PROXY_CONFIG=/etc/sandbox-proxy/config.json \
    PROXY_CA_DIR=/ca
VOLUME ["/ca"]
EXPOSE 3128
ENTRYPOINT ["proxy-entrypoint.sh"]
