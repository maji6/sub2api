LINKED_ENV := deploy/.env.linked
LINKED_ENV_EXAMPLE := deploy/.env.linked.example
LINKED_COMPOSE := docker compose --env-file $(LINKED_ENV) -f deploy/docker-compose.linked.yml
BUNDLE_ENV := deploy/.env.bundle
BUNDLE_ENV_EXAMPLE := deploy/.env.bundle.example
BUNDLE_COMPOSE := docker compose --env-file $(BUNDLE_ENV) -f deploy/docker-compose.bundle.yml

run:
	go run ./cmd/sub2api-audit

test:
	go test ./...

tidy:
	go mod tidy

build:
	go build -o bin/sub2api-audit ./cmd/sub2api-audit

stack-init:
	@if [ ! -f "$(LINKED_ENV)" ]; then \
		cp "$(LINKED_ENV_EXAMPLE)" "$(LINKED_ENV)"; \
		echo "Created $(LINKED_ENV) from $(LINKED_ENV_EXAMPLE)."; \
	fi

stack-up: stack-init
	$(LINKED_COMPOSE) up -d --build

stack-down:
	$(LINKED_COMPOSE) down

stack-logs:
	$(LINKED_COMPOSE) logs -f

stack-ps:
	$(LINKED_COMPOSE) ps

bundle-init:
	@if [ ! -f "$(BUNDLE_ENV)" ]; then \
		cp "$(BUNDLE_ENV_EXAMPLE)" "$(BUNDLE_ENV)"; \
		echo "Created $(BUNDLE_ENV) from $(BUNDLE_ENV_EXAMPLE)."; \
		echo "Update SUB2API_IMAGE and SUB2API_AUDIT_IMAGE before first production run."; \
	fi

bundle-up: bundle-init
	$(BUNDLE_COMPOSE) up -d

bundle-down:
	$(BUNDLE_COMPOSE) down

bundle-logs:
	$(BUNDLE_COMPOSE) logs -f

bundle-ps:
	$(BUNDLE_COMPOSE) ps

bundle-pull: bundle-init
	$(BUNDLE_COMPOSE) pull
