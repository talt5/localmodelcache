# Code/manifest generation and artifact sync.

##@ Code generation

# --fail keeps an HTTP error body out of the downloaded artifact; 5xx and timeouts are
# retried so a flaky release download does not fail the whole generation run.
CURL ?= curl --fail --silent --show-error --location --retry 3 --retry-delay 2

.PHONY: sync-img-env
sync-img-env:
	@python3 hack/setup/scripts/generate-images-sh.py

generate-chart-manifests: helm-docs ## After config/ or charts/_common edits: regenerate chart files, then helm lint. Needs helm.
	@bash hack/setup/scripts/generate_chart_manifests.sh
	make lint-helm-charts
	@# Chart READMEs are rendered from each chart's values.yaml, which the script above
	@# regenerates from charts/_common. Run helm-docs here, after the sync, so a change to a
	@# common value is documented on the first precommit pass rather than the second.
	$(HELM_DOCS) --chart-search-root=charts --output-file=README.md

manifests: controller-gen kustomize yq ## After API type or RBAC marker changes: CRDs, RBAC, deepcopy, chart CRDs.
	@$(CONTROLLER_GEN) $(CRD_OPTIONS) paths=./pkg/apis/cache/... output:crd:dir=config/crd/full
	@$(CONTROLLER_GEN) rbac:roleName=localmodelcache-manager-role paths=./pkg/controller/v1alpha1/localmodel output:rbac:artifacts:config=config/rbac/localmodel
	@$(CONTROLLER_GEN) rbac:roleName=localmodelcache-kernelcache-nodegroup-manager paths=./pkg/controller/v1alpha1/kernelcache output:rbac:artifacts:config=config/rbac/kernelcache
	@$(CONTROLLER_GEN) rbac:roleName=localmodelcache-node-agent-role paths=./pkg/controller/v1alpha1/localmodelnode output:rbac:artifacts:config=config/rbac/localmodelnode
	@$(CONTROLLER_GEN) rbac:roleName=localmodelcache-kernelcache-agent-role paths=./pkg/controller/v1alpha1/kernelcachenode output:rbac:artifacts:config=config/rbac/kernelcachenode
	# Hook for distro-specific manifest generation (override via Makefile.overrides.mk).
	@$(MAKE) manifests-distro

	# controller-gen writes every CRD to config/crd/full; sort them into their component folders.
	mv config/crd/full/local.cache.ai_clusterstoragecontainers.yaml config/crd/full/clusterstoragecontainer/
	mv config/crd/full/local.cache.ai_localmodelcaches.yaml config/crd/full/localmodel/
	mv config/crd/full/local.cache.ai_localmodelnamespacecaches.yaml config/crd/full/localmodel/
	mv config/crd/full/local.cache.ai_localmodelnodegroups.yaml config/crd/full/localmodel/
	mv config/crd/full/local.cache.ai_localmodelnodes.yaml config/crd/full/localmodel/
	mv config/crd/full/local.cache.ai_kernelcaches.yaml config/crd/full/kernelcache/
	mv config/crd/full/local.cache.ai_kernelcachenodes.yaml config/crd/full/kernelcache/
	mv config/crd/full/local.cache.ai_kernelcachecaptures.yaml config/crd/full/kernelcache/
	mv config/crd/full/local.cache.ai_kernelcachenodegroups.yaml config/crd/full/kernelcache/

	@# An existing deepcopy file keeps its copyright year, a new one gets CURRENT_YEAR.
	@dir=pkg/apis/cache/v1alpha1; \
	year=$$(awk '/^Copyright [0-9]+ The KServe Authors/ { print $$2; exit }' "$$dir/zz_generated.deepcopy.go" 2>/dev/null); \
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.deepcopy.go.txt,year="$${year:-$(CURRENT_YEAR)}" paths="./$$dir"

	# envtest loads every CRD from test/crds.
	@$(KUSTOMIZE) build config/crd/full > test/crds/local.cache.ai_all_crds.yaml
	@for chart in localmodelcache-crd; do \
		for dir in localmodel kernelcache clusterstoragecontainer; do \
			cp -f config/crd/full/$$dir/local.cache.ai_*.yaml charts/$$chart/templates/; \
		done; \
	done

	./hack/minimal-crdgen.sh
	@for dir in localmodel kernelcache clusterstoragecontainer; do \
		cp -f config/crd/minimal/$$dir/local.cache.ai_*.yaml charts/localmodelcache-crd-minimal/templates/; \
	done

generate: manifests ## After API type changes: same as manifests (deepcopy is generated there).

uv-lock: $(UV) ## After Python dependency changes: re-lock python/storage/uv.lock. Needs network.
	cd ./python/storage && $(UV) lock

# Sync multi-resource helpers to charts that need them (must run before helm package)
sync-helm-multi-resource-helpers:
	@echo "Syncing multi-resource helpers to charts..."
	@for chart in localmodelcache; do \
		cp charts/_common/_resources.tpl charts/$$chart/templates/_resources.tpl; \
		echo "  ✓ Copied to charts/$$chart/templates/_resources.tpl"; \
	done

boilerplate:
	hack/boilerplate.sh

# Extension point for distro-specific manifest generation.
.PHONY: manifests-distro
manifests-distro:
