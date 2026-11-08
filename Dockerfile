# syntax=docker/dockerfile:1

FROM golang:1.27.3-alpine AS build-base

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# Only production inputs invalidate compilation. Browser fixtures, Go tests,
# documentation and CI metadata are excluded by .dockerignore.
COPY cmd ./cmd
COPY internal ./internal
COPY *.go ./
COPY deploy ./deploy
COPY examples/configs ./examples/configs

# Both binaries share one solve/cache across the three bake targets. Normal
# layers hold modules and compiled objects so GHA can export them: BuildKit
# cache mounts alone are not included in the external layer cache.
FROM build-base AS build-objects
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath ./cmd/control-plane ./cmd/agent

FROM build-objects AS build-binaries
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" -o /out/ ./cmd/control-plane ./cmd/agent \
    && mv /out/control-plane /out/qcontrol-plane \
    && mv /out/agent /out/qagent

FROM alpine:3.24 AS runtime-base

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

COPY --from=build-binaries /out/qcontrol-plane /usr/local/bin/qcontrol-plane
COPY --from=build-binaries /out/qagent /usr/local/lib/qcontrolhub/qagent
COPY deploy/remote/install-agent.sh /usr/local/lib/qcontrolhub/install-agent.sh

ENV QCH_AGENT_BINARY_PATH=/usr/local/lib/qcontrolhub/qagent
ENV QCH_AGENT_INSTALLER_PATH=/usr/local/lib/qcontrolhub/install-agent.sh

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/qcontrol-plane"]

FROM runtime-base AS qagent

COPY --from=build-binaries /out/qagent /usr/local/bin/qagent

VOLUME ["/var/lib/qcontrolhub"]
ENTRYPOINT ["/usr/local/bin/qagent"]

FROM nginx:1.31-alpine AS qcontrol-web

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/qimaoww/qcontrolhub" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="GPL-3.0-only"

COPY frontend/index.html /usr/share/nginx/html/index.html
COPY frontend/bootstrap.js /usr/share/nginx/html/assets/bootstrap.js
COPY frontend/favicon.svg /usr/share/nginx/html/assets/favicon.svg
COPY frontend/analytics /usr/share/nginx/html/assets/analytics
# Keep the shipped shared-module graph aligned with the active theme.
# frontend/module_policy_test.mjs verifies this list transitively.
COPY frontend/modules/account-request.js \
    frontend/modules/account-storage.js \
    frontend/modules/agent-address-model.js \
    frontend/modules/agent-batch.js \
    frontend/modules/agent-service-state.js \
    frontend/modules/agent-time-zone-model.js \
    frontend/modules/archive-draft-model.js \
    frontend/modules/card-move-scroll.js \
    frontend/modules/certificate-order.js \
    frontend/modules/client-access-model.js \
    frontend/modules/client-connection-cache.js \
    frontend/modules/client-connection-model.js \
    frontend/modules/code-format.js \
    frontend/modules/config-defaults.js \
    frontend/modules/config-field-model.js \
    frontend/modules/config-file-model.js \
    frontend/modules/config-mutations.js \
    frontend/modules/config-outbound-model.js \
    frontend/modules/config-snapshot.js \
    frontend/modules/core-install-source.js \
    frontend/modules/core-install-state.js \
    frontend/modules/core-log-cache.js \
    frontend/modules/core-log-model.js \
    frontend/modules/core-log-preferences.js \
    frontend/modules/core-log-projection.js \
    frontend/modules/core-log-read.js \
    frontend/modules/core-log-scope.js \
    frontend/modules/core-log-selection.js \
    frontend/modules/core-log-source-status.js \
    frontend/modules/dashboard-model.js \
    frontend/modules/enrollment-command.js \
    frontend/modules/errors.js \
    frontend/modules/inbound-options-model.js \
    frontend/modules/ip-quality-controller.js \
    frontend/modules/ip-quality-model.js \
    frontend/modules/live-config-state.js \
    frontend/modules/node-order.js \
    frontend/modules/outbound-preset-controller.js \
    frontend/modules/outbound-preset-model.js \
    frontend/modules/permissions.js \
    frontend/modules/poller.js \
    frontend/modules/preset-route.js \
    frontend/modules/record-display.js \
    frontend/modules/region-model.js \
    frontend/modules/route-loader.js \
    frontend/modules/route-warmup.js \
    frontend/modules/routes.js \
    frontend/modules/session-watch.js \
    frontend/modules/shared-instance-model.js \
    frontend/modules/task-model.js \
    frontend/modules/substore-model.js \
    frontend/modules/system-bbr-model.js \
    frontend/modules/system-bbr-presets.js \
    frontend/modules/theme-lifecycle.js \
    frontend/modules/theme-registry.js \
    frontend/modules/traffic-model.js \
    frontend/modules/user-model.js \
    frontend/modules/xray-api.js \
    /usr/share/nginx/html/assets/modules/
COPY frontend/startup.css /usr/share/nginx/html/assets/startup.css
COPY deploy/remote/install-agent.sh /usr/share/nginx/html/install-agent.sh
COPY deploy/bootstrap-core-services.sh /usr/share/nginx/html/install-assets/deploy/bootstrap-core-services.sh
COPY deploy/existing-core-mapping.sh /usr/share/nginx/html/install-assets/deploy/existing-core-mapping.sh
COPY deploy/openrc /usr/share/nginx/html/install-assets/deploy/openrc
COPY deploy/systemd/qagent-core-journal.conf /usr/share/nginx/html/install-assets/deploy/systemd/qagent-core-journal.conf
COPY deploy/systemd/qagent-mihomo.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-mihomo.service
COPY deploy/systemd/qagent-xray.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-xray.service
COPY deploy/systemd/qagent-sing-box.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-sing-box.service
COPY deploy/systemd/qagent-shadowsocks-rust.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-shadowsocks-rust.service
COPY deploy/systemd/qagent-mihomo@.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-mihomo@.service
COPY deploy/systemd/qagent-xray@.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-xray@.service
COPY deploy/systemd/qagent-sing-box@.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-sing-box@.service
COPY deploy/systemd/qagent-shadowsocks-rust@.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent-shadowsocks-rust@.service
COPY deploy/systemd/qagent.service /usr/share/nginx/html/install-assets/deploy/systemd/qagent.service
COPY examples/configs /usr/share/nginx/html/install-assets/examples/configs
COPY frontend/nginx.conf /etc/nginx/nginx.conf
# Parse the config during the build: the build sandbox has no compose DNS and
# /etc is read-only, so the one literal upstream is pinned to loopback in a
# copy. Directive-level regressions from an nginx jump fail here, not at deploy.
RUN sed 's#http://control-plane:8080#http://127.0.0.1:8080#g' /etc/nginx/nginx.conf > /tmp/nginx.check.conf \
    && nginx -t -c /tmp/nginx.check.conf \
    && rm -f /tmp/nginx.check.conf
RUN startup_css_version="$(sha256sum /usr/share/nginx/html/assets/startup.css | cut -c1-16)" \
    && analytics_css_version="$(sha256sum /usr/share/nginx/html/assets/analytics/theme.css | cut -c1-16)" \
    && js_content_version="$(find /usr/share/nginx/html/assets -type f -name '*.js' -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-10)" \
    && js_version="${js_content_version}-$(printf '%s' "${VERSION}" | sha256sum | cut -c1-10)" \
    && find /usr/share/nginx/html/assets -name '*.js' -exec sed -i -E \
      -e "s#(from[[:space:]]+)(['\"])(\\.\\.?/[^'\"?]+\\.js)['\"]#\\1\\2\\3?v=${js_version}\\2#g" \
      -e "s#(import\\([[:space:]]*)(['\"])(\\.\\.?/[^'\"?]+\\.js)['\"]#\\1\\2\\3?v=${js_version}\\2#g" {} + \
    && sed -i \
      -e "s/__QCH_STARTUP_CSS_VERSION__/${startup_css_version}/g" \
      -e "s/__QCH_ANALYTICS_CSS_VERSION__/${analytics_css_version}/g" \
      -e "s/__QCH_JS_VERSION__/${js_version}/g" \
      /usr/share/nginx/html/index.html \
    && find /usr/share/nginx/html/assets -type f \( -name '*.js' -o -name '*.css' \) -exec gzip -9 -k {} +

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD wget -q -O - http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["nginx", "-g", "daemon off;"]
