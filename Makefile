.PHONY: help build test tidy fmt vet lint \
        dev-up dev-down dev-logs migrate \
        run-api run-controller run-builder run-gateway run-triggers \
        dev-cluster dev-cluster-down crds-apply crds-delete \
        helm-lint helm-template \
        web-install web-dev web-build

BIN_DIR := bin
SERVICES := api controller builder gateway triggers

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

build: ## Build all services
	@mkdir -p $(BIN_DIR)
	@for svc in $(SERVICES); do \
		echo ">> building $$svc"; \
		go build -o $(BIN_DIR)/$$svc ./cmd/$$svc; \
	done

test: ## Run all Go tests
	go test ./...

tidy: ## Tidy Go modules
	go mod tidy

fmt: ## Format Go code
	go fmt ./...

vet: ## Run go vet
	go vet ./...

lint: fmt vet ## fmt + vet

dev-up: ## Start local Postgres + NATS via docker-compose
	docker compose -f scripts/docker-compose.dev.yml up -d

dev-down: ## Stop local dev dependencies
	docker compose -f scripts/docker-compose.dev.yml down

dev-logs: ## Tail local dev dependency logs
	docker compose -f scripts/docker-compose.dev.yml logs -f

migrate: ## Apply SQL migrations to the local Postgres
	@for f in migrations/*.sql; do \
		echo ">> applying $$f"; \
		docker exec -i ailab-postgres psql -U ailab -d ailab -v ON_ERROR_STOP=1 < $$f; \
	done

run-api: ## Run api against local docker-compose dependencies
	go run ./cmd/api

run-controller: ## Run controller against current kubectl context
	go run ./cmd/controller

run-builder: ## Run builder against local deps
	go run ./cmd/builder

run-gateway: ## Run gateway locally
	go run ./cmd/gateway

run-triggers: ## Run triggers locally
	go run ./cmd/triggers

dev-cluster: ## Create local kind cluster
	kind create cluster --name ailab --config scripts/kind-config.yaml

dev-cluster-down: ## Delete local kind cluster
	kind delete cluster --name ailab

crds-apply: ## Apply CRDs to current kubectl context
	kubectl apply -f deploy/helm/agent-platform/templates/crd-agentrun.yaml
	kubectl apply -f deploy/helm/agent-platform/templates/crd-agentdeployment.yaml

crds-delete: ## Delete CRDs
	kubectl delete -f deploy/helm/agent-platform/templates/crd-agentrun.yaml --ignore-not-found
	kubectl delete -f deploy/helm/agent-platform/templates/crd-agentdeployment.yaml --ignore-not-found

helm-lint: ## Lint the Helm chart
	helm lint deploy/helm/agent-platform
	helm lint deploy/helm/agent-platform -f deploy/helm/agent-platform/values-saas.yaml
	helm lint deploy/helm/agent-platform -f deploy/helm/agent-platform/values-selfhost.yaml

helm-template: ## Render the Helm chart for both profiles
	@echo "== saas =="
	helm template ailab deploy/helm/agent-platform -f deploy/helm/agent-platform/values-saas.yaml
	@echo "== selfhost =="
	helm template ailab deploy/helm/agent-platform -f deploy/helm/agent-platform/values-selfhost.yaml

web-install: ## Install Next.js UI deps
	cd web && npm install

web-dev: ## Run the Next.js UI in dev mode on :3000
	cd web && npm run dev

web-build: ## Production-build the Next.js UI
	cd web && npm run build
