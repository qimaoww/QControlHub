SHELL := /bin/sh

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test alpine-test vet fmt-check frontend-check pr-policy-test installer-test quick-start-test web-image-test docs-check check init-env compose-config up dev-up down logs

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags '$(LDFLAGS)' -o bin/qcontrol-plane ./cmd/control-plane
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags '$(LDFLAGS)' -o bin/qagent ./cmd/agent

test:
	go test ./...

alpine-test:
	@packages="$$(go list ./... | sed '\|/internal/agent$$|d')"; go test $$packages
	go test ./internal/agent -run 'OpenRC|PerServiceManager'

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { printf '%s\n' 'Go files need formatting; run gofmt -w on the listed files:'; gofmt -l .; exit 1; }

frontend-check:
	node frontend/module_smoke.mjs
	node frontend/agents_browser_smoke.mjs

pr-policy-test:
	node --test .github/scripts/check-pr.test.mjs

installer-test:
	sh deploy/tests/inherit-existing-core.sh

quick-start-test:
	bash deploy/tests/quick-start-env.sh
	bash deploy/tests/quick-start-bootstrap.sh

web-image-test:
	docker build --target qcontrol-web --build-arg VERSION='$(VERSION)' .

docs-check:
	node docs/check_docs.mjs

check: fmt-check frontend-check pr-policy-test installer-test quick-start-test docs-check vet test

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
