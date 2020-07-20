# k8s_gateway

Similar to [k8s_external](https://coredns.io/plugins/k8s_external/) but supporting all types of Kubernetes external resources - Ingress, Service of type LoadBalancer and `networking.x-k8s.io/Gateway` (when it becomes available).

This plugin relies on it's own connection to the k8s APi server (using client-go) and doesn't share any code with the existing [kubernetes](https://coredns.io/plugins/kubernetes/) plugin. The assumption is that this plugin can now be deployed as a separate instance (alongside the internal kube-dns) and act as a single external DNS interface into your Kubernetes cluster(s).

## Description

`k8s_gateway` resolves Kubernetes resources with their external IP addresses. Based on zones specified in the configuration, this plugin will resolve the following type of resources:

| Kind | Matching Against | External IPs are from | 
| ---- | ---------------- | -------- |
| Ingress | all FQDNs from `spec.rules[*].host` matching specified zones | `.status.loadBalancer.ingress` |
| Service[^1] | `name.namespace` + any of the specified zones | `.status.loadBalancer.ingress` | 

[^1]: Only resolves service type LoadBalancer

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
$ git clone https://github.com/coredns/coredns
$ cd coredns
$ vim plugin.cfg
# Add the line alias:github.com/serverwentdown/alias before the file middleware
$ go generate
$ go build
$ ./coredns -plugins | grep alias
```

### With external golang source code
```
$ git clone https://github.com/serverwentdown/alias
$ cd k8s_gateway
$ go build cmd/coredns.go
$ ./coredns -plugins | grep alias
```

For more details refer to [this CoreDNS doc](https://coredns.io/2017/07/25/compile-time-enabling-or-disabling-plugins/)



## Also see

TODO: Blogpost


## TODO

[] Readme
[] Blogpost
[] Tilt cleanup https://github.com/kubernetes-sigs/cluster-api/blob/master/Tiltfile