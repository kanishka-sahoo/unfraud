SHELL := /usr/bin/env bash
export PATH := /usr/local/go/bin:$(HOME)/go/bin:$(PATH)

CLUSTER   := unfraud
NAMESPACE := unfraud
CHART     := deploy/helm/unfraud
VALUES    ?= $(CHART)/values.yaml
TAG       ?= dev
CMDS      := $(notdir $(wildcard cmd/*))

.PHONY: help data gen lint test test-integration reset-topic featurize train parity report accuracy system-run check-cases fixtures build images deploy-model secrets console tailscale grafana cluster-up cluster-down deploy replay kafka-wait

help: ## List targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

data: ## Download datasets with your Kaggle credentials and verify checksums
	./scripts/fetch-data.sh

gen: ## Regenerate Protobuf code
	buf generate

lint: ## Lint Go, Protobuf and the Helm chart
	go vet ./...
	test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }
	buf lint
	helm lint $(CHART)

test: ## Run unit tests
	go test ./...

test-integration: ## Run tests against the cluster's Kafka (needs make cluster-up deploy)
	UNFRAUD_KAFKA_BROKERS=localhost:30092 go test -count=1 ./...

reset-topic: ## Delete and recreate a Profile's input topic: make reset-topic PROFILE=elliptic
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	kubectl -n $(NAMESPACE) delete kafkatopic input.$(PROFILE) --wait
	helm upgrade unfraud $(CHART) -n $(NAMESPACE) -f $(VALUES) --set image.tag=$(TAG) --reset-then-reuse-values
	kubectl -n $(NAMESPACE) wait kafkatopic/input.$(PROFILE) --for=condition=Ready --timeout=120s

featurize: ## Accuracy Run to training tables: make featurize PROFILE=ieee-cis
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	go run ./cmd/featurize -profile $(PROFILE)

train: ## Train every feature set of a Profile: make train PROFILE=ieee-cis
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	@for fs in $(if $(filter elliptic,$(PROFILE)),local flow dataset_agg,static realtime graph); do \
	  uv run --project train unfraud-train train --features data/features/$(PROFILE) \
	    --out models/$(PROFILE)/lgbm-$$fs --feature-set $$fs || exit 1; \
	done

parity: ## Check Go scores match Python on the test month: make parity PROFILE=ieee-cis [SET=realtime]
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	UNFRAUD_PARITY_MODEL=models/$(PROFILE)/lgbm-$(or $(SET),realtime) UNFRAUD_PARITY_FEATURES=data/features/$(PROFILE) \
	  go test -count=1 -run TestParityWithPython -v ./internal/model/

report: ## Regenerate a Profile's results tables: make report PROFILE=ieee-cis
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	uv run --project train unfraud-train report --models models/$(PROFILE) --out results/$(PROFILE)-lightgbm --features data/features/$(PROFILE)

accuracy: ## Accuracy Run through the scorer: make accuracy PROFILE=ieee-cis MODEL=lgbm-realtime
	@test -n "$(PROFILE)" -a -n "$(MODEL)" || { echo "PROFILE and MODEL are required"; exit 1; }
	go run ./cmd/accuracy -profile $(PROFILE) -model models/$(PROFILE)/$(MODEL)

system-run: ## Score the whole input topic in the cluster from clean state and compare: make system-run PROFILE=ieee-cis
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	./scripts/system-run.sh $(PROFILE) models/$(PROFILE)/lgbm-realtime/decisions.parquet results/$(PROFILE)-system-run-aligned.json

check-cases: ## Check Case invariants against stored Decisions
	./scripts/check-cases.sh

fixtures: ## Regenerate the Go evaluator's LightGBM fixtures
	uv run --project train unfraud-train fixture --out internal/model/lgbm/testdata

build: ## Build all commands into bin/
	go build -o bin/ ./cmd/...

images: ## Build images and load them into the kind cluster
	docker build -q -f deploy/docker/Dockerfile.web -t unfraud/web:$(TAG) . && kind load docker-image --name $(CLUSTER) unfraud/web:$(TAG)
	@for c in $(CMDS); do \
	  docker build -q -f deploy/docker/Dockerfile --build-arg CMD=$$c -t unfraud/$$c:$(TAG) . && \
	  kind load docker-image --name $(CLUSTER) unfraud/$$c:$(TAG); \
	done

deploy-model: ## Ship a model as an image and run it: make deploy-model PROFILE=ieee-cis [MODEL=lgbm-realtime] [SERVICE=scorer|graph]
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	$(eval DIR := models/$(PROFILE)/$(or $(MODEL),lgbm-realtime))
	$(eval IMG := unfraud/model-$(PROFILE):$(shell sha256sum $(DIR)/model.txt | cut -c1-12))
	docker build -q -f deploy/docker/Dockerfile.model -t $(IMG) $(DIR)
	kind load docker-image --name $(CLUSTER) $(IMG)
	helm upgrade unfraud $(CHART) -n $(NAMESPACE) -f $(VALUES) --reset-then-reuse-values --set image.tag=$(TAG) \
	  --set-string '$(or $(SERVICE),scorer).models.$(PROFILE)=$(IMG)'
	kubectl -n $(NAMESPACE) rollout status deploy/$(subst graph,graphsvc,$(or $(SERVICE),scorer))-$(PROFILE) --timeout=300s

secrets: ## Load the OpenRouter key from ~/.config/unfraud/openrouter_key into the cluster
	@test -s $(HOME)/.config/unfraud/openrouter_key || { echo "put your OpenRouter key in ~/.config/unfraud/openrouter_key (chmod 600)"; exit 1; }
	kubectl -n $(NAMESPACE) create secret generic openrouter --from-file=api-key=$(HOME)/.config/unfraud/openrouter_key \
	  --dry-run=client -o yaml | kubectl apply -f -

console: ## Open the web console on http://localhost:3000 (port-forward; new clusters also map :30080)
	kubectl -n $(NAMESPACE) port-forward svc/web 3000:3000

tailscale: ## Serve the console (:3000) and Grafana (:3001) on this machine's Tailscale address, across reboots
	./scripts/tailscale-expose.sh install

grafana: ## Open Grafana on http://localhost:3001
	kubectl -n $(NAMESPACE) port-forward svc/grafana 3001:3000

cluster-up: ## Create the kind cluster and install operators
	./deploy/kind/up.sh

cluster-down: ## Delete the kind cluster and everything in it
	kind delete cluster --name $(CLUSTER)

deploy: ## Install or upgrade the chart (VALUES=$(CHART)/values-full.yaml for full)
	helm upgrade --install unfraud $(CHART) -n $(NAMESPACE) --create-namespace -f $(VALUES) --set image.tag=$(TAG)
	$(MAKE) kafka-wait

kafka-wait:
	kubectl -n $(NAMESPACE) wait kafka/unfraud --for=condition=Ready --timeout=600s

replay: ## Replay a dataset into Kafka in the cluster: make replay PROFILE=elliptic [SPEED=0] [LIMIT=0]
	@test -n "$(PROFILE)" || { echo "PROFILE is required"; exit 1; }
	$(eval RUN := $(shell date +%Y%m%d%H%M%S))
	helm template unfraud $(CHART) -n $(NAMESPACE) -f $(VALUES) --set image.tag=$(TAG) \
	  --set replay.profile=$(PROFILE) --set-string replay.runId=$(RUN) \
	  --set replay.speed=$(or $(SPEED),0) --set replay.limit=$(or $(LIMIT),0) \
	  --show-only templates/replay-job.yaml | kubectl -n $(NAMESPACE) apply -f -
	kubectl -n $(NAMESPACE) wait job/replay-$(PROFILE)-$(RUN) --for=condition=complete --timeout=3600s & \
	  kubectl -n $(NAMESPACE) logs -f job/replay-$(PROFILE)-$(RUN) --pod-running-timeout=120s; wait
