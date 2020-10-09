load('ext://restart_process', 'docker_build_with_restart')

IMG = 'localhost:5000/coredns'

def binary():
    return "CGO_ENABLED=0  GOOS=linux GOATCH=amd64 GO111MODULE=on go build cmd/coredns.go"

local_resource('recompile', binary(), deps=['cmd', 'gateway.go', 'kubernetes.go', 'setup.go', 'apex.go'])

docker_build_with_restart(IMG, '.', 
    dockerfile='tilt.Dockerfile', 
    entrypoint=['/coredns'], 
    only=['./coredns'], 
    live_update=[
        sync('./coredns', '/coredns'),
        ]
    )


k8s_kind("kind")

# CoreDNS with updated RBAC
k8s_yaml('./test/kubernetes.yaml')

# Baremetal ingress controller (nodeport-based)
k8s_yaml('./test/ingress.yaml')

# Metallb
k8s_yaml('./test/metallb.yaml')
