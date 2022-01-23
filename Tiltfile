load('ext://restart_process', 'docker_build_with_restart')
load('ext://helm_remote', 'helm_remote')

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
k8s_yaml(helm(
    './charts/k8s-gateway',
    namespace="kube-system",
    name='excoredns',
    values=['./test/k8s-gateway-values.yaml'],
    )
)

# Baremetal ingress controller (nodeport-based)
helm_remote('ingress-nginx',
            version="4.0.15",
            repo_name='ingress-nginx',
            set=['controller.admissionWebhooks.enabled=false'],
            repo_url='https://kubernetes.github.io/ingress-nginx')

# Backend deployment for testing
k8s_yaml('./test/backend.yml')

# Metallb
helm_remote('metallb',
            version="0.11.0",
            repo_name='metallb',
            values=['./test/metallb-values.yaml'],
            repo_url='https://metallb.github.io/metallb')

# Nginxinc kubernetes-ingress
k8s_kind('VirtualServer', api_version='k8s.nginx.org/v1')
helm_remote('nginx-ingress',
            version="0.12.0",
            release_name="nginxinc",
            repo_name='nginx-stable',
            values=['./test/nginxinc-kubernetes-ingress/values.yaml'],
            repo_url='https://helm.nginx.com/stable')


# Gateway API
k8s_kind('HTTPRoute', api_version='gateway.networking.k8s.io/v1alpha2')
k8s_kind('Gateway', api_version='gateway.networking.k8s.io/v1alpha2')
k8s_yaml('./test/gateway-api/crds.yml')


helm_remote('istiod',
            version="1.12.1",
            repo_name='istio',
            set=['global.istioNamespace=default', 'base.enableIstioConfigCRDs=false', 'telemetry.enabled=false'],
            repo_url='https://istio-release.storage.googleapis.com/charts')
helm_remote('gateway',
            version="1.12.1",
            repo_name='istio',
            namespace='default',
            repo_url='https://istio-release.storage.googleapis.com/charts')
