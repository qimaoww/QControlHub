# syntax=docker/dockerfile:1.7

FROM golang:1.25.14-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build-base

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

# Export exactly the executable copied into the runtime images for signing.
# A host-side rebuild may use another Go toolchain and produce another digest.
FROM scratch AS agent-release
COPY --from=build-qagent /out/qagent /qagent

FROM alpine:3.22 AS runtime-base

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/qimaoww/qcontrolhub" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="GPL-3.0-only"

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S qcontrolhub \
    && adduser -S -D -H -G qcontrolhub qcontrolhub \
    && install -d -o qcontrolhub -g qcontrolhub /var/lib/qcontrolhub

USER qcontrolhub:qcontrolhub
WORKDIR /var/lib/qcontrolhub
STOPSIGNAL SIGTERM

FROM runtime-base AS qcontrol-plane

COPY --from=build-qcontrol-plane /out/qcontrol-plane /usr/local/bin/qcontrol-plane
COPY --from=build-qagent /out/qagent /usr/local/lib/qcontrolhub/qagent
COPY deploy/remote/install-agent.sh /usr/local/lib/qcontrolhub/install-agent.sh

ENV QCH_AGENT_BINARY_PATH=/usr/local/lib/qcontrolhub/qagent
ENV QCH_AGENT_INSTALLER_PATH=/usr/local/lib/qcontrolhub/install-agent.sh

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
      org.opencontainers.image.licenses="GPL-3.0-only"

COPY frontend/index.html /usr/share/nginx/html/index.html
COPY frontend/app.js /usr/share/nginx/html/assets/app.js
COPY frontend/modules /usr/share/nginx/html/assets/modules
COPY frontend/app.css /usr/share/nginx/html/assets/app.css
COPY deploy/remote/install-agent.sh /usr/share/nginx/html/install-agent.sh
COPY deploy/bootstrap-core-services.sh /usr/share/nginx/html/install-assets/deploy/bootstrap-core-services.sh
COPY deploy/existing-core-mapping.sh /usr/share/nginx/html/install-assets/deploy/existing-core-mapping.sh
COPY deploy/openrc /usr/share/nginx/html/install-assets/deploy/openrc
COPY deploy/systemd/qagent-core-journal.conf /usr/share/nginx/html/install-assets/deploy/systemd/qagent-core-journal.conf
COPY deploy/systemd/qagent-mihomo.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-mihomo.service
COPY deploy/systemd/qagent-xray.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-xray.service
COPY deploy/systemd/qagent-sing-box.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-sing-box.service
COPY deploy/systemd/qagent-shadowsocks-rust.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-shadowsocks-rust.service
COPY deploy/systemd/qagent.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent.service
COPY examples/configs /usr/share/nginx/html/install-assets/examples/configs
# The signed release artifacts. Sign them before building this image (see
# docs/release-signing.md): an installer that pins QCH_RELEASE_PUBLIC_KEY fetches
# SHA256SUMS beside the assets and refuses to continue without it, so the files
# have to be part of the image rather than generated at run time.
ARG RELEASE_ARTIFACTS=dist/release
COPY ${RELEASE_ARTIFACTS}/SHA256SUMS /usr/share/nginx/html/install-assets/SHA256SUMS
COPY ${RELEASE_ARTIFACTS}/SHA256SUMS.sig /usr/share/nginx/html/install-assets/SHA256SUMS.sig
COPY frontend/nginx.conf /etc/nginx/nginx.conf
RUN css_version="$(sha256sum /usr/share/nginx/html/assets/app.css | cut -c1-16)" \
    && js_content_version="$(find /usr/share/nginx/html/assets -type f -name '*.js' -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-10)" \
    && js_version="${js_content_version}-$(printf '%s' "${VERSION}" | sha256sum | cut -c1-10)" \
    && sed -i -E \
      -e "s#(from \"\\./modules/[^\"]+\\.js)\"#\\1?v=${js_version}\"#g" \
      -e "s#(import\\(\"\\./modules/[^\"]+\\.js)\"\\)#\\1?v=${js_version}\"\\)#g" \
      /usr/share/nginx/html/assets/app.js \
    && find /usr/share/nginx/html/assets/modules -name '*.js' -exec sed -i -E "s#(from \"\\./[^\"]+\\.js)\"#\\1?v=${js_version}\"#g" {} + \
    && sed -i \
      -e "s/__QCH_CSS_VERSION__/${css_version}/g" \
      -e "s/__QCH_JS_VERSION__/${js_version}/g" \
      /usr/share/nginx/html/index.html \
    && find /usr/share/nginx/html/assets -type f \( -name '*.js' -o -name '*.css' \) -exec gzip -9 -k {} +

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD wget -q -O - http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["nginx", "-g", "daemon off;"]
