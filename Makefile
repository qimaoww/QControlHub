SHELL := /bin/sh

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test release-checksums signing-key check-install-assets alpine-test alpine-agent-test upgrade-sandbox-test ss-rust-runtime-test vet fmt-check frontend-check pr-policy-test schema-policy-test installer-test agent-redeploy-test quick-start-test web-image-test docs-check check checks browser-checks non-browser-checks alpine-non-browser-checks init-env compose-config up dev-up down logs

# Non-Go checks. CI runs each group as its own task next to the Go test shards,
# so keep the Go suite out of these targets.
BROWSER_CHECK_TARGETS := frontend-check
CHECK_TARGETS := fmt-check pr-policy-test schema-policy-test installer-test agent-redeploy-test quick-start-test check-install-assets docs-check vet
# Alpine validates the same checks without the Debian-only agent redeploy flow.
ALPINE_CHECK_TARGETS := fmt-check pr-policy-test schema-policy-test installer-test quick-start-test check-install-assets docs-check vet
# Alpine leaves internal/agent out of the package sweep and runs the OpenRC and
# lifecycle regressions instead; the upgrade sandbox job covers the rest.
ALPINE_AGENT_TESTS := go test ./internal/agent -run 'OpenRC|PerServiceManager|AgentUpgrade|ManagedCorePrerequisites|SystemBBR'

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags '$(LDFLAGS)' -o bin/qcontrol-plane ./cmd/control-plane
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags '$(LDFLAGS)' -o bin/qagent ./cmd/agent
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags '$(LDFLAGS)' -o bin/release-sign ./cmd/release-sign

# Schemas isolate data, not PostgreSQL's database-wide vacuum horizon.
# Keep packages sequential so migration tests cannot pin HOT-page measurements.
test:
	go test -p 1 ./...

alpine-agent-test:
	$(ALPINE_AGENT_TESTS)

alpine-test: alpine-agent-test
	@packages="$$(go list ./... | sed '\|/internal/agent$$|d')"; go test -p 1 $$packages

upgrade-sandbox-test:
	docker build -f deploy/tests/Dockerfile.upgrade-lifecycle -t qch-upgrade-lifecycle-test .
	docker run --rm --read-only --network none --cap-drop ALL --cap-add CHOWN \
		--security-opt no-new-privileges --tmpfs /tmp:rw,exec,mode=1777 \
		--tmpfs /usr/local/lib/qagent:rw,exec,mode=0755 --tmpfs /etc/qagent:rw,mode=0755 \
		--tmpfs /etc/systemd:rw,mode=0755 --tmpfs /var/lib/qcontrolhub-shadowsocks-rust:rw,mode=0750 \
		-e QCH_TEST_UPGRADE_SANDBOX=1 -e GOCACHE=/tmp/qch-go-cache -e GOTOOLCHAIN=local \
		-v "$(CURDIR):/src:ro" -v "$$(go env GOMODCACHE):/go/pkg/mod:ro" \
		qch-upgrade-lifecycle-test go test ./internal/agent -run '^(TestAgentLifecycleReadOnlySandbox|TestSSRustMigrationLifecycleWithACLAndActiveManagedService|TestImportResourcesReuseAndRollbackOwnership)$$' -count=1 -v

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { printf '%s\n' 'Go files need formatting; run gofmt -w on the listed files:'; gofmt -l .; exit 1; }

frontend-check:
	node frontend/module_smoke.mjs
	node frontend/agents_browser_smoke.mjs

ss-rust-runtime-test:
	sh deploy/tests/ss-rust-runtime.sh

pr-policy-test:
	node --test .github/scripts/check-pr.test.mjs

schema-policy-test:
	node --test .github/scripts/check-schema-version.test.mjs

installer-test:
	sh deploy/tests/inherit-existing-core.sh

agent-redeploy-test:
	sh deploy/tests/install-agent-redeploy.sh

quick-start-test:
	bash deploy/tests/quick-start-ready.sh
	bash deploy/tests/quick-start-env.sh
	bash deploy/tests/quick-start-bootstrap.sh
	bash deploy/tests/quick-start-update.sh
	bash deploy/tests/quick-start-modes.sh
	bash deploy/tests/quick-start-compose.sh

# Signed release artifacts. The web image embeds SHA256SUMS and its signature so
# an installer that pins QCH_RELEASE_PUBLIC_KEY can verify every file it places
# on a node. Generate them before building the image:
#   make signing-key                     (once, on the release machine)
#   make release-checksums RELEASE_KEY=release.key
# The private key stays on the release machine; only dist/release/ is published.
# The signed list is also the list of files a node downloads from
# /install-assets/, so it has to name exactly what the web image serves: a listed
# path that is not served fails the install with a 404, and a served asset that is
# not listed is placed on the node unverified. deploy/tests/release-assets.sh
# derives that file list from the installer and refuses to answer when the two
# disagree, so a new download cannot quietly fall outside the signature.
RELEASE_DIR ?= dist/release
RELEASE_KEY ?= release.key

# One-time release keypair. Keep the private key on the release machine or in a
# protected CI environment: a key that reaches the control plane would let whoever
# takes the control plane over sign a replacement for every downloaded file.
signing-key: build
	@test ! -e $(RELEASE_KEY) || { printf '%s\n' '$(RELEASE_KEY) already exists; refusing to overwrite it'; exit 1; }
	./bin/release-sign keygen -private $(RELEASE_KEY) -public $(RELEASE_KEY).pub
	./bin/release-sign pubkey -public $(RELEASE_KEY).pub -out $(RELEASE_KEY).pub.pem
	@printf '%s\n' 'publish $(RELEASE_KEY).pub.pem and set QCH_RELEASE_PUBLIC_KEY to it on each node'

check-install-assets:
	bash deploy/tests/release-assets.sh --check

release-checksums: build check-install-assets
	@assets="$$(bash deploy/tests/release-assets.sh --files | sed 's/^/-assets /' | tr '\n' ' ')" || exit 1; \
	./bin/release-sign checksums -key $(RELEASE_KEY) -agent bin/qagent $$assets \
		-out $(RELEASE_DIR) || exit 1; \
	./bin/release-sign sign -key $(RELEASE_KEY) -release '$(VERSION)' \
		-agent bin/qagent -agent-version '$(VERSION)' \
		$$assets -out $(RELEASE_DIR)/release-manifest.json

web-image-test:
	docker build --target qcontrol-web --build-arg VERSION='$(VERSION)' .

docs-check:
	node docs/check_docs.mjs

check: $(BROWSER_CHECK_TARGETS) $(CHECK_TARGETS) test

# CI runs the same checks in parallel groups; these targets keep one source of
# truth for the targets that "make check" covers.
checks: $(BROWSER_CHECK_TARGETS) $(CHECK_TARGETS)
browser-checks: $(BROWSER_CHECK_TARGETS)
non-browser-checks: $(CHECK_TARGETS)
alpine-non-browser-checks: $(ALPINE_CHECK_TARGETS)

init-env:
	@command -v openssl >/dev/null 2>&1 || { printf '%s\n' 'openssl is required'; exit 1; }
	@test ! -e .env || { printf '%s\n' '.env already exists; refusing to overwrite it'; exit 1; }
	@umask 077; \
	db_password="$$(openssl rand -hex 32)"; \
	admin_token="$$(openssl rand -hex 32)"; \
	admin_token_sha256="$$(printf '%s' "$$admin_token" | openssl dgst -sha256 | awk '{print $$NF}')"; \
	webhook_secret="$$(openssl rand -hex 32)"; \
	config_key="$$(openssl rand -hex 32)"; \
	mkdir -p .secrets; \
	chmod 0700 .secrets; \
	printf '%s\n' "$$config_key" > .secrets/config-encryption-key; \
	printf '\n' > .secrets/config-encryption-previous-keys; \
	chmod 0644 .secrets/config-encryption-key .secrets/config-encryption-previous-keys; \
	printf '%s\n' \
		'POSTGRES_DB=qcontrolhub' \
		'POSTGRES_USER=qcontrolhub' \
		"POSTGRES_PASSWORD=$$db_password" \
		'POSTGRES_PORT=5432' \
		'QCH_DATABASE_BIND_ADDRESS=127.0.0.1' \
		'QCH_ADMIN_TOKEN=' \
		"QCH_ADMIN_TOKEN_SHA256=$$admin_token_sha256" \
		"QCH_WEBHOOK_SECRET=$$webhook_secret" \
		'QCH_CONFIG_ENCRYPTION_KEY=' \
		'QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=' \
		'QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE=.secrets/config-encryption-key' \
		'QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE=.secrets/config-encryption-previous-keys' \
		'QCH_BEHIND_TLS_PROXY=true' \
		'QCH_ALLOW_INSECURE_HTTP=false' \
		'QCH_ALLOW_INSECURE_DATABASE=true' \
		'QCH_CORS_ORIGINS=https://qcontrolhub.example.com' \
		'QCH_CONTROL_PROXY_SUBNET=172.30.254.0/24' \
		'QCH_CONTROL_PROXY_GATEWAY=172.30.254.1' \
		'QCH_WEB_PROXY_ADDRESS=172.30.254.2' \
		'QCH_CONTROL_PLANE_PROXY_ADDRESS=172.30.254.3' \
		'QCH_TRUSTED_PROXY_CIDRS=172.30.254.2/32,172.30.254.1/32' \
		'QCH_BIND_ADDRESS=127.0.0.1' \
		'QCH_PORT=8080' \
		'QCH_IMAGE_TAG=latest' \
		'VERSION=$(VERSION)' > .env; \
	bash -c 'source deploy/quick-start.sh; write_secret_compose_override'; \
	printf '%s\n' '.env created with mode 0600; raw administrator token is not stored.'; \
	printf '%s\n' "Administrator token (shown once): $$admin_token"; \
	printf '%s\n' 'Store it in a password manager now.'

compose-config:
	docker compose -f docker-compose.yml -f docker-compose.secrets.yml config --quiet

up:
	docker compose -f docker-compose.yml -f docker-compose.secrets.yml pull
	docker compose -f docker-compose.yml -f docker-compose.secrets.yml up -d

dev-up:
	QCH_IMAGE_TAG=local QCH_BEHIND_TLS_PROXY=false QCH_ALLOW_INSECURE_HTTP=true QCH_ALLOW_INSECURE_DATABASE=true docker compose -f docker-compose.yml -f docker-compose.secrets.yml up -d --build

down:
	docker compose -f docker-compose.yml -f docker-compose.secrets.yml down

logs:
	docker compose -f docker-compose.yml -f docker-compose.secrets.yml logs -f qcontrol-web control-plane postgres
