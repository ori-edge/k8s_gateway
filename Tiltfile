

def local_build():
    local("CGO_ENABLED=0  go build cmd/coredns.go")


local_build()
docker_build('localhost:5000/coredns', '.', dockerfile='./DockerfileTilt')


k8s_kind("kind")

# CoreDNS with updated RBAC
k8s_yaml('./test/kubernetes.yaml')

# Baremetal ingress controller (nodeport-based)
k8s_yaml('./test/ingress.yaml')

# Metallb
k8s_yaml('./test/metallb.yaml')

watch_file("./gateway.go")
watch_file("./kubernetes.go")
watch_file("./cmd/*.go")