module github.com/ori-edge/k8s_gateway

go 1.18

require (
	github.com/coredns/caddy v1.1.1
	github.com/coredns/coredns v1.10.1
	github.com/miekg/dns v1.1.53
	github.com/nginxinc/kubernetes-ingress v1.12.5
	k8s.io/api v0.26.3
	k8s.io/apimachinery v0.26.3
	k8s.io/client-go v0.26.3
	sigs.k8s.io/gateway-api v0.4.3
)

require (
	github.com/Azure/go-autorest/autorest/adal v0.9.20 // indirect
	go.uber.org/zap v1.19.0 // indirect
)

// https://github.com/etcd-io/etcd/issues/12124
replace google.golang.org/grpc v1.43.0 => google.golang.org/grpc v1.29.1
