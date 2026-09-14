SHELL := /bin/bash
LOCALBIN ?= $(shell pwd)/bin

.PHONY: generate
generate: controller-gen
	$(LOCALBIN)/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: manifests
manifests: controller-gen
	$(LOCALBIN)/controller-gen crd paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: envtest-bin
envtest-bin: envtest
	$(LOCALBIN)/setup-envtest use -p path 1.31.x

.PHONY: test
test: manifests generate gateway-api-crds
	KUBEBUILDER_ASSETS="$$($(LOCALBIN)/setup-envtest use -p path 1.31.x)" go test ./... -count=1

.PHONY: build
build:
	go build -o bin/manager ./cmd/manager

.PHONY: gateway-api-crds
gateway-api-crds:
	mkdir -p config/crd/gateway-api
	curl -sL https://raw.githubusercontent.com/kubernetes-sigs/gateway-api/v1.1.0/config/crd/standard/gateway.networking.k8s.io_httproutes.yaml \
		-o config/crd/gateway-api/httproutes.yaml

controller-gen:
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

envtest:
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
