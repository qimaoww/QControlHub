# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS build-base

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

FROM build-base AS build-qcontrol-plane

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -buildvcs=false \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/qcontrol-plane \
    ./cmd/control-plane

FROM build-base AS build-qagent

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -buildvcs=false \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/qagent \
    ./cmd/agent

FROM alpine:3.22 AS runtime-base

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/qimaoww/qcontrolhub" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="Proprietary"

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S qcontrolhub \
    && adduser -S -D -H -G qcontrolhub qcontrolhub \
    && install -d -o qcontrolhub -g qcontrolhub /var/lib/qcontrolhub

USER qcontrolhub:qcontrolhub
WORKDIR /var/lib/qcontrolhub
STOPSIGNAL SIGTERM

FROM runtime-base AS qcontrol-plane

COPY --from=build-qcontrol-plane /out/qcontrol-plane /usr/local/bin/qcontrol-plane

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/qcontrol-plane"]

FROM runtime-base AS qagent

COPY --from=build-qagent /out/qagent /usr/local/bin/qagent

VOLUME ["/var/lib/qcontrolhub"]
ENTRYPOINT ["/usr/local/bin/qagent"]

FROM nginx:1.27-alpine AS qcontrol-web

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/qimaoww/qcontrolhub" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="Proprietary"

COPY frontend/index.html /usr/share/nginx/html/index.html
COPY frontend/app.js /usr/share/nginx/html/assets/app.js
COPY frontend/app.css /usr/share/nginx/html/assets/app.css
COPY frontend/nginx.conf /etc/nginx/nginx.conf
RUN css_version="$(sha256sum /usr/share/nginx/html/assets/app.css | cut -c1-16)" \
    && js_version="$(sha256sum /usr/share/nginx/html/assets/app.js | cut -c1-16)" \
    && sed -i \
      -e "s/__QCH_CSS_VERSION__/${css_version}/g" \
      -e "s/__QCH_JS_VERSION__/${js_version}/g" \
      /usr/share/nginx/html/index.html

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD wget -q -O - http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["nginx", "-g", "daemon off;"]
