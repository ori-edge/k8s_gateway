up:
	-./test/kind-with-registry.sh &>/dev/null
	tilt up

down: 
	kind delete cluster --name gateway

build:
	go build cmd/coredns.go

.PHONY: test
test:
	go test -cover ./...

clean:
	go clean
	rm -f coredns
