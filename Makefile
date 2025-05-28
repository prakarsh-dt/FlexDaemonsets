# Image and controller name settings
IMG ?= flexdaemonsets-webhook:latest
CONTROLLER_IMG ?= $(IMG) # Keeping it simple, IMG is the main image we build
WEBHOOK_NAMESPACE ?= flexdaemonsets-system
CERT_DIR ?= ./_certs # Directory to store generated certs locally

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN = $(shell go env GOPATH)/bin
endif

# Setting SHELL to bash allows bash commands to be used in recipes.
SHELL = /usr/bin/env bash

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire Makefile.
.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manager
manager: ## Build the manager binary.
	@echo "Building manager binary..."
	CGO_ENABLED=0 go build -o bin/manager cmd/manager/main.go

.PHONY: run
run: manager ## Run the manager locally (requires certs in CERT_DIR).
	@echo "Running manager locally..."
	@echo "Make sure you have TLS certificates in $(CERT_DIR) or specify a different --cert-dir."
	./bin/manager --cert-dir=$(CERT_DIR) --metrics-bind-address=localhost:8080 --health-probe-bind-address=localhost:8081

.PHONY: generate-certs
generate-certs: ## Generate self-signed TLS certificates for local testing.
	@echo "Generating self-signed TLS certificates in $(CERT_DIR)..."
	@mkdir -p $(CERT_DIR)
	@openssl genrsa -out $(CERT_DIR)/ca.key 2048
	@openssl req -x509 -new -nodes -key $(CERT_DIR)/ca.key -subj "/CN=flexdaemonsets.xai" -days 365 -out $(CERT_DIR)/ca.crt
	@openssl genrsa -out $(CERT_DIR)/tls.key 2048
	@openssl req -new -key $(CERT_DIR)/tls.key -subj "/CN=flexdaemonsets-webhook-svc.$(WEBHOOK_NAMESPACE).svc" -out $(CERT_DIR)/tls.csr
	@openssl x509 -req -in $(CERT_DIR)/tls.csr -CA $(CERT_DIR)/ca.crt -CAkey $(CERT_DIR)/ca.key -CAcreateserial -out $(CERT_DIR)/tls.crt -days 365 -sha256 -extfile <(printf "subjectAltName=DNS:flexdaemonsets-webhook-svc.$(WEBHOOK_NAMESPACE).svc,DNS:flexdaemonsets-webhook-svc.$(WEBHOOK_NAMESPACE).svc.cluster.local")
	@echo "Certificates generated in $(CERT_DIR): ca.crt, tls.crt, tls.key"
	@echo "To use them, create a secret e.g.:"
	@echo "  kubectl create secret tls flexdaemonsets-webhook-tls \"
	@echo "    --cert=$(CERT_DIR)/tls.crt \"
	@echo "    --key=$(CERT_DIR)/tls.key \"
	@echo "    -n $(WEBHOOK_NAMESPACE)"


##@ Build

.PHONY: build
build: manager ## Build manager binary.
	@echo "Manager binary built at bin/manager."

# Ensure the image name is defined
ifndef IMAGE_TAG_BASE
	IMAGE_TAG_BASE = prakarshmodificationusg/flexdaemonsets-webhook
endif
IMG ?= ${IMAGE_TAG_BASE}:latest

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	@echo "Building Docker image $(IMG)..."
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push docker image.
	@echo "Pushing Docker image $(IMG)..."
	docker push $(IMG)


##@ Deployment (requires kubectl configured for a cluster)

# KUSTOMIZE ?= $(GOBIN)/kustomize
KUBECTL ?= kubectl

.PHONY: deploy-manifests
deploy-manifests: ## Apply all manifests in the manifests directory.
	@echo "Applying manifests from ./manifests directory..."
	$(KUBECTL) apply -f ./manifests/
	@echo "Waiting for CRD to be established..."
	@while ! $(KUBECTL) get crd flexdaemonsettemplates.flexdaemonsets.xai > /dev/null 2>&1; do \
	  echo "  Waiting for CRD flexdaemonsettemplates.flexdaemonsets.xai..."; \
	  sleep 1; \
	done
	@echo "CRD flexdaemonsettemplates.flexdaemonsets.xai is established."
	@echo "Important: Ensure the 'caBundle' in 'manifests/webhook.yaml' is correctly set or injected (e.g., by cert-manager)."
	@echo "If using self-signed certs (via make generate-certs), update caBundle with content of $(CERT_DIR)/ca.crt (base64 encoded)."
	@echo "You may also need to restart existing pods if the webhook is re-deployed with changes that affect them."

.PHONY: deploy
deploy: manager docker-build deploy-manifests ## Build, Docker build, and deploy all manifests.
	@echo "Deployment initiated. Use 'make docker-push' if you need to push the image to a registry."
	@echo "To complete deployment if not using cert-manager for caBundle injection:"
	@echo "1. Ensure your Docker image $(IMG) is available to your cluster (e.g., pushed to a registry)."
	@echo "2. Create the TLS secret 'flexdaemonsets-webhook-tls' in namespace '$(WEBHOOK_NAMESPACE)' using your generated certs (see 'make generate-certs' output)."
	@echo "3. Base64 encode $(CERT_DIR)/ca.crt and update 'caBundle' in 'manifests/webhook.yaml'."
	@echo "4. Re-apply 'manifests/webhook.yaml': $(KUBECTL) apply -f manifests/webhook.yaml"
	@echo "5. Update the image in 'manifests/deployment.yaml' to $(IMG) if it's not already set, then $(KUBECTL) apply -f manifests/deployment.yaml"


.PHONY: undeploy-manifests
undeploy-manifests: ## Delete all manifests in the manifests directory.
	@echo "Deleting manifests from ./manifests directory..."
	$(KUBECTL) delete -f ./manifests/ --ignore-not-found=true

.PHONY: undeploy
undeploy: undeploy-manifests ## Delete all deployed resources.
	@echo "Undeployment complete."
	@echo "Note: This does not delete the CRD instances, only the definition and webhook components."
	@echo "To delete the CRD (and all its custom resources): $(KUBECTL) delete crd flexdaemonsettemplates.flexdaemonsets.xai"

.PHONY: clean-certs
clean-certs: ## Remove locally generated TLS certificates.
	@echo "Removing local certificates from $(CERT_DIR)..."
	@rm -rf $(CERT_DIR)

.PHONY: clean
clean: clean-certs ## Remove build artifacts and local certificates.
	@echo "Cleaning up build artifacts..."
	@rm -f bin/manager
	@echo "Cleanup complete."
