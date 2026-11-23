# Local development: build binaries, deploy/undeploy.

##@ Local development

build: fmt vet ## Build bin/localmodel, bin/localmodelnode and bin/kernelcachenode.
	go build -o bin/localmodel ./cmd/localmodel
	go build -o bin/localmodelnode ./cmd/localmodelnode
	go build -o bin/kernelcachenode ./cmd/kernelcachenode

deploy: manifests ## Install config/default (CRDs, controller, node agents) with kustomize. Needs cert-manager.
	kubectl apply --server-side=true --force-conflicts -k config/crd
	kubectl wait --for=condition=established --timeout=60s crd/localmodelcaches.local.cache.ai
	kubectl apply --server-side=true -k config/default
	kubectl wait --for=condition=available deployment/localmodelcache-controller-manager -n localmodelcache --timeout=300s

undeploy: ## Delete config/default from the cluster, CRDs included.
	kubectl delete -k config/default

deploy-helm: ## Install the CRD and resources charts from charts/ with Helm. Needs cert-manager.
	helm upgrade -i localmodelcache-crd charts/localmodelcache-crd -n localmodelcache --create-namespace --wait
	helm upgrade -i localmodelcache charts/localmodelcache -n localmodelcache --wait $(HELM_EXTRA_ARGS)

undeploy-helm: ## Uninstall the Helm releases deploy-helm installed.
	helm uninstall localmodelcache -n localmodelcache
	helm uninstall localmodelcache-crd -n localmodelcache

deploy-cert-manager: ## Install cert-manager with Helm, which issues the webhook certificate.
	hack/setup/infra/manage.cert-manager-helm.sh
