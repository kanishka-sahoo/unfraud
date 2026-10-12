SHELL := /usr/bin/env bash
export PATH := /usr/local/go/bin:$(HOME)/go/bin:$(PATH)

CLUSTER   := unfraud
NAMESPACE := unfraud
CHART     := deploy/helm/unfraud
VALUES    ?= $(CHART)/values.yaml
TAG       ?= dev
CMDS      := $(notdir $(wildcard cmd/*))

.PHONY: help data gen lint test test-integration reset-topic build images cluster-up cluster-down deploy replay kafka-wait

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
	helm upgrade unfraud $(CHART) -n $(NAMESPACE) -f $(VALUES) --set image.tag=$(TAG) --reuse-values
	kubectl -n $(NAMESPACE) wait kafkatopic/input.$(PROFILE) --for=condition=Ready --timeout=120s

build: ## Build all commands into bin/
	go build -o bin/ ./cmd/...

images: ## Build images and load them into the kind cluster
	@for c in $(CMDS); do \
	  docker build -q -f deploy/docker/Dockerfile --build-arg CMD=$$c -t unfraud/$$c:$(TAG) . && \
	  kind load docker-image --name $(CLUSTER) unfraud/$$c:$(TAG); \
	done

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
