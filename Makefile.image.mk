# Container image build and push (engine set by ENGINE, default docker).

##@ Images

docker-build: docker-build-localmodel docker-build-localmodelnode-agent docker-build-kernelcachenode-agent ## Build the controller and node agent images as $KO_DOCKER_REPO/<name>:$TAG. Other images: docker-{build,push}-<name>.

docker-push: docker-push-localmodel docker-push-localmodelnode-agent docker-push-kernelcachenode-agent ## Build and push the controller and node agent images.

docker-build-localmodel:
	${ENGINE} buildx build ${ARCH} --load --build-arg GOTAGS=${GOTAGS} -t ${KO_DOCKER_REPO}/${LOCALMODEL_CONTROLLER_IMG}:${TAG} -f localmodel.Dockerfile .

docker-push-localmodel: docker-build-localmodel
	${ENGINE} buildx build ${ARCH} --push --build-arg GOTAGS=${GOTAGS} -t ${KO_DOCKER_REPO}/${LOCALMODEL_CONTROLLER_IMG}:${TAG} -f localmodel.Dockerfile .

docker-build-localmodelnode-agent:
	${ENGINE} buildx build ${ARCH} --load --build-arg GOTAGS=${GOTAGS} -t ${KO_DOCKER_REPO}/${LOCALMODEL_AGENT_IMG}:${TAG} -f localmodel-agent.Dockerfile .

docker-push-localmodelnode-agent: docker-build-localmodelnode-agent
	${ENGINE} buildx build ${ARCH} --push --build-arg GOTAGS=${GOTAGS} -t ${KO_DOCKER_REPO}/${LOCALMODEL_AGENT_IMG}:${TAG} -f localmodel-agent.Dockerfile .

docker-build-kernelcachenode-agent:
	${ENGINE} buildx build ${ARCH} --load --build-arg GOTAGS=${GOTAGS} -t ${KO_DOCKER_REPO}/${KERNELCACHE_AGENT_IMG}:${TAG} -f kernelcache-agent.Dockerfile .

docker-push-kernelcachenode-agent: docker-build-kernelcachenode-agent
	${ENGINE} buildx build ${ARCH} --push --build-arg GOTAGS=${GOTAGS} -t ${KO_DOCKER_REPO}/${KERNELCACHE_AGENT_IMG}:${TAG} -f kernelcache-agent.Dockerfile .

docker-build-storageInitializer: ## Build the model download image from python/storage.
	cd python && ${ENGINE} buildx build ${ARCH} --load --build-arg BASE_IMAGE=${BASE_IMG} -t ${KO_DOCKER_REPO}/${STORAGE_INIT_IMG}:${TAG} -f storage-initializer.Dockerfile .

docker-push-storageInitializer: docker-build-storageInitializer
	${ENGINE} push ${KO_DOCKER_REPO}/${STORAGE_INIT_IMG}:${TAG}

docker-build-mcv-minimal:
	${ENGINE} buildx build ${ARCH} --target mcv-minimal -t ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-minimal -f kernelcache/mcv/mcv.Dockerfile .

docker-push-mcv-minimal: docker-build-mcv-minimal
	${ENGINE} push ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-minimal

docker-build-mcv-rocm:
	${ENGINE} buildx build ${ARCH} --target mcv-rocm -t ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-rocm -f kernelcache/mcv/mcv.Dockerfile .

docker-push-mcv-rocm: docker-build-mcv-rocm
	${ENGINE} push ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-rocm

docker-build-mcv-gaudi:
	${ENGINE} buildx build ${ARCH} --target mcv-gaudi -t ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-gaudi -f kernelcache/mcv/mcv.Dockerfile .

docker-push-mcv-gaudi: docker-build-mcv-gaudi
	${ENGINE} push ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-gaudi

docker-build-mcv-cuda:
	${ENGINE} buildx build ${ARCH} --target mcv-cuda -t ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-cuda -f kernelcache/mcv/mcv.Dockerfile .

docker-push-mcv-cuda: docker-build-mcv-cuda
	${ENGINE} push ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-cuda

docker-build-mcv-unified:
	${ENGINE} buildx build ${ARCH} --target mcv-unified -t ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-unified -f kernelcache/mcv/mcv.Dockerfile .

docker-push-mcv-unified: docker-build-mcv-unified
	${ENGINE} push ${KO_DOCKER_REPO}/${MCV_IMG}:${TAG}-unified

docker-build-mcv: docker-build-mcv-minimal docker-build-mcv-rocm docker-build-mcv-gaudi docker-build-mcv-cuda docker-build-mcv-unified ## Build every MCV kernel capture image variant.

docker-push-mcv: docker-push-mcv-minimal docker-push-mcv-rocm docker-push-mcv-gaudi docker-push-mcv-cuda docker-push-mcv-unified

# The bundle holds linux/amd64 images, so it takes much longer on other architectures.
.PHONY: airgapped-bundle
airgapped-bundle: ## Build the air-gapped bundle of this version in OUT (default dist). Needs Docker, Helm and network.
	hack/airgapped/build.sh $(or $(OUT),dist)
