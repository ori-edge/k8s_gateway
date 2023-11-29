allow_k8s_contexts('colima')
allow_k8s_contexts('local')

# using others with the makefile
load('ext://restart_process', 'docker_build_with_restart')
load('ext://helm_remote', 'helm_remote')

IMG = 'localhost:5000/coredns'

def binary():
    return "CGO_ENABLED=0  GOOS=linux GOARCH=amd64 GO111MODULE=on go build cmd/coredns.go"

local_resource('recompile', binary(), deps=['cmd', 'gateway.go', 'kubernetes.go', 'setup.go', 'apex.go'])


docker_build_with_restart(IMG, '.',
    dockerfile='tilt.Dockerfile',
    entrypoint=['/coredns'], 
    live_update=[
        sync('./coredns', '/coredns'),
        ]
)

# Cilium CNI
helm_remote('cilium',
            version="1.15.0-pre.1",
            namespace="kube-system",
            repo_name='cilium',
            values=['./test/cilium/helm-values.yaml'],
            repo_url='https://helm.cilium.io')
k8s_yaml('./test/cilium/dual-stack/crd-values.yaml')


# CoreDNS with updated RBAC
k8s_yaml(helm(
    './charts/k8s-gateway',
    namespace="kube-system",
    name='excoredns',
    values=['./test/dual-stack/k8s-gateway-values.yaml'],
    )
)

# Baremetal ingress controller (nodeport-based)
helm_remote('ingress-nginx',
            version="4.8.3",
            repo_name='ingress-nginx',
            set=['controller.admissionWebhooks.enabled=false'],
            repo_url='https://kubernetes.github.io/ingress-nginx')

# Nginxinc kubernetes-ingress
helm_remote('nginx-ingress',
            version="1.0.1",
            release_name="nginxinc",
            repo_name='oci://ghcr.io/nginxinc/charts',
            values=['./test/nginxinc-kubernetes-ingress/values.yaml']
            )
k8s_kind('VirtualServer', api_version='k8s.nginx.org/v1')

helm_remote('istiod',
            version="1.19.3",
            repo_name='istio',
            set=['global.istioNamespace=default', 'base.enableIstioConfigCRDs=false', 'telemetry.enabled=false'],
            repo_url='https://istio-release.storage.googleapis.com/charts')
helm_remote('gateway',
            version="1.19.3",
            repo_name='istio',
            namespace='default',
            repo_url='https://istio-release.storage.googleapis.com/charts')

# Backend deployment for testing
k8s_yaml('./test/backend.yml')

# gateway-apis
k8s_yaml('./test/gateway-api/crds.yml')

# Gateway API
k8s_kind('HTTPRoute', api_version='gateway.networking.k8s.io/v1')
k8s_kind('TLSRoute', api_version='gateway.networking.k8s.io/v1alpha2')
k8s_kind('GRPCRoute', api_version='gateway.networking.k8s.io/v1alpha2')
k8s_kind('Gateway', api_version='gateway.networking.k8s.io/v1')
k8s_yaml('./test/gateway-api/resources.yml')
k8s_yaml('./test/gatewayclasses.yaml')
k8s_yaml('./test/dual-stack/service-annotation.yml')
k8s_yaml('./test/dual-stack/ingress-services.yml')
