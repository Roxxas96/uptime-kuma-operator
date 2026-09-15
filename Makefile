SHELL := /bin/bash
LOCALBIN ?= $(shell pwd)/bin

.PHONY: generate
generate: controller-gen
	$(LOCALBIN)/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: manifests
manifests: controller-gen
	$(LOCALBIN)/controller-gen crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
	$(MAKE) sync-chart-crds

# The Helm chart ships the CRD in crds/, which Helm installs before templates
# and never templates. It must be a byte-for-byte copy of the generated CRD,
# so `manifests` always refreshes it rather than leaving it to drift.
.PHONY: sync-chart-crds
sync-chart-crds:
	mkdir -p charts/uptime-kuma-operator/crds
	cp config/crd/bases/uptime-kuma.io_monitors.yaml charts/uptime-kuma-operator/crds/uptime-kuma.io_monitors.yaml

.PHONY: envtest-bin
envtest-bin: envtest
	$(LOCALBIN)/setup-envtest use -p path 1.31.x

.PHONY: test
test: manifests generate gateway-api-crds
	KUBEBUILDER_ASSETS="$$($(LOCALBIN)/setup-envtest use -p path 1.31.x)" go test ./... -count=1

.PHONY: build
build:
	go build -o bin/manager ./cmd

.PHONY: gateway-api-crds
gateway-api-crds:
	mkdir -p config/crd/gateway-api
	curl -sL https://raw.githubusercontent.com/kubernetes-sigs/gateway-api/v1.6.2/config/crd/standard/gateway.networking.k8s.io_httproutes.yaml \
		-o config/crd/gateway-api/httproutes.yaml

controller-gen:
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0

envtest:
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.1

# Tools below are pinned in mise.toml (`mise install` before first use).

.PHONY: fmt
fmt:
	golangci-lint fmt

.PHONY: lint
lint:
	go vet ./...
	golangci-lint run

.PHONY: vuln
vuln:
	govulncheck ./...

.PHONY: pre-commit-all
pre-commit-all:
	pre-commit run --all-files
