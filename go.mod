module github.com/ori-edge/k8s_gateway

go 1.16

require (
	github.com/coredns/caddy v1.1.0
	github.com/coredns/coredns v1.8.3
	github.com/miekg/dns v1.1.41
	google.golang.org/grpc v1.43.0 // indirect
	k8s.io/api v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v0.22.1
	sigs.k8s.io/gateway-api v0.4.0
)

// https://github.com/etcd-io/etcd/issues/12124
replace google.golang.org/grpc v1.43.0 => google.golang.org/grpc v1.29.1
