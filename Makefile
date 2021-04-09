# The binary to build (just the basename).
BIN := $(shell basename $$PWD)
COMMIT := $(shell git describe --dirty --always)
TAG := $(shell git describe --tags --dirty || echo latest)
LDFLAGS := "-s -w -X github.com/coredns/coredns/coremain.GitCommit=$(COMMIT)"
ARCHS := "linux/amd64,linux/arm64"

# Where to push the docker image.
REGISTRY ?= quay.io/oriedge


# Image URL to use all building/pushing image targets
IMG ?= $(REGISTRY)/$(BIN)

setup:
	-./test/kind-with-registry.sh &>/dev/null

up: setup
	tilt up

down:
	tilt down

nuke: 
	-./test/teardown-kind-with-registry.sh &>/dev/null

## Build the plugin binary
build:
	CGO_ENABLED=0 go build -ldflags $(LDFLAGS) cmd/coredns.go


## Generate new helm package and update chart yaml file
helm-update:
	helm package charts/k8s-gateway -d charts/
	helm repo index --url https://ori-edge.github.io/k8s_gateway/ --merge index.yaml .

.PHONY: test
test:
	go test -race ./... -short

clean:
	go clean
	rm -f coredns

## Build the docker image
docker: test
	docker buildx build --push \
		--build-arg LDFLAGS=$(LDFLAGS) \
		--platform ${ARCHS} \
		-t ${IMG}:${COMMIT} \
		.


## Release the current code with git tag  and `latest`
release: test
	docker buildx build --push \
		--build-arg LDFLAGS=$(LDFLAGS) \
		--platform ${ARCHS} \
		-t ${IMG}:${TAG} \
		-t ${IMG}:latest \
		.

# From: https://gist.github.com/klmr/575726c7e05d8780505a
help:
	@echo "$$(tput sgr0)";sed -ne"/^## /{h;s/.*//;:d" -e"H;n;s/^## //;td" -e"s/:.*//;G;s/\\n## /---/;s/\\n/ /g;p;}" ${MAKEFILE_LIST}|awk -F --- -v n=$$(tput cols) -v i=15 -v a="$$(tput setaf 6)" -v z="$$(tput sgr0)" '{printf"%s%*s%s ",a,-i,$$1,z;m=split($$2,w," ");l=n-i;for(j=1;j<=m;j++){l-=length(w[j])+1;if(l<= 0){l=n-i-length(w[j])-1;printf"\n%*s ",-i," ";}printf"%s ",w[j];}printf"\n";}'
