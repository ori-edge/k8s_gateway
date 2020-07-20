# k8s_gateway

A CoreDNS plugin to resolve all types of external Kubernetes resources. Similar to [k8s_external](https://coredns.io/plugins/k8s_external/) but supporting all types of Kubernetes external resources - Ingress, Service of type LoadBalancer and `networking.x-k8s.io/Gateway` (when it becomes available).

This plugin relies on it's own connection to the k8s APi server and doesn't share any code with the existing [kubernetes](https://coredns.io/plugins/kubernetes/) plugin. The assumption is that this plugin can now be deployed as a separate instance (alongside the internal kube-dns) and act as a single external DNS interface into your Kubernetes cluster(s).

## Description

`k8s_gateway` resolves Kubernetes resources with their external IP addresses. Based on zones specified in the configuration, this plugin will resolve the following type of resources:

| Kind | Matching Against | External IPs are from | 
| ---- | ---------------- | -------- |
| Ingress | all FQDNs from `spec.rules[*].host` matching specified zones | `.status.loadBalancer.ingress` |
| Service[*] | `name.namespace` + any of the specified zones | `.status.loadBalancer.ingress` | 

[*]: Only resolves service of type LoadBalancer

Currently only supports A-type queries, all other queries result in NODATA responses.

This plugin is **NOT** supposed to be used for intra-cluster DNS resolution and does not contain the default upstream [kubernetes](https://coredns.io/plugins/kubernetes/) plugin.

## Configure

```
k8s_gateway [ZONE...] 
```

Optionally, you can specify what kind of resources to watch and the default TTL to return in response, e.g.

```
k8s_gateway example.com {
    resources Ingress
    ttl 10
}
```

## Build

### With compile-time configuration file

```
$ git clone https://github.com/ori-edge/k8s_external
$ cd k8s_external
$ vim plugin.cfg
# Replace lines with kubernetes and k8s_external with k8s_external:github.com/ori-edge/k8s_external
$ go generate
$ go build
$ ./coredns -plugins | grep k8s_external
```

### With external golang source code
```
$ git clone https://github.com/ori-edge/k8s_external
$ cd k8s_gateway
$ go build cmd/coredns.go
$ ./coredns -plugins | grep k8s_external
```

For more details refer to [this CoreDNS doc](https://coredns.io/2017/07/25/compile-time-enabling-or-disabling-plugins/)



## Also see

TODO: Blogpost
