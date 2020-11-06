# k8s_gateway

A CoreDNS plugin that is very similar to [k8s_external](https://coredns.io/plugins/k8s_external/) but supporting all types of Kubernetes external resources - Ingress, Service of type LoadBalancer and `networking.x-k8s.io/Gateway` (when it becomes available). 

This plugin relies on it's own connection to the k8s API server and doesn't share any code with the existing [kubernetes](https://coredns.io/plugins/kubernetes/) plugin. The assumption is that this plugin can now be deployed as a separate instance (alongside the internal kube-dns) and act as a single external DNS interface into your Kubernetes cluster(s).

## Description

`k8s_gateway` resolves Kubernetes resources with their external IP addresses based on zones specified in the configuration. This plugin will resolve the following type of resources:

| Kind | Matching Against | External IPs are from | 
| ---- | ---------------- | -------- |
| Ingress | all FQDNs from `spec.rules[*].host` matching configured zones | `.status.loadBalancer.ingress` |
| Service[*] | `name.namespace` + any of the configured zones | `.status.loadBalancer.ingress` | 

[*]: Only resolves service of type LoadBalancer

Currently only supports A-type queries, all other queries result in NODATA responses.

This plugin is **NOT** supposed to be used for intra-cluster DNS resolution and does not contain the default upstream [kubernetes](https://coredns.io/plugins/kubernetes/) plugin.

## Install

The recommended installation method is using the helm chart provided in the repo:

```
helm install exdns --set domain=foo ./charts/k8s-gateway
```

Alternatively, for labbing and testing purposes `k8s_gateway` can be deployed with a single manifest:

```
kubectl apply -f https://github.com/ori-edge/k8s_gateway/blob/master/examples/install-clusterwide.yml
```

## Configure

```
k8s_gateway [ZONE...] 
```

Optionally, you can specify what kind of resources to watch, default TTL to return in response and a default name to use for zone apex, e.g.

```
k8s_gateway example.com {
    resources Ingress
    ttl 10
    apex dns1
}
```

## Build

### With compile-time configuration file

```
$ git clone https://github.com/coredns/coredns
$ cd coredns
$ vim plugin.cfg
# Replace lines with kubernetes and k8s_external with k8s_gateway:github.com/ori-edge/k8s_gateway
$ go generate
$ go build
$ ./coredns -plugins | grep k8s_gateway
```

### With external golang source code
```
$ git clone https://github.com/ori-edge/k8s_external
$ cd k8s_gateway
$ go build cmd/coredns.go
$ ./coredns -plugins | grep k8s_external
```

For more details refer to [this CoreDNS doc](https://coredns.io/2017/07/25/compile-time-enabling-or-disabling-plugins/)


## Hack

This repository contains a [Tiltfile](https://tilt.dev/) that can be used for local development. To setup a local environment do:

```
make up
```

Some test resources can be added to the k8s cluster with:

```
kubectl apply -f ./test/test.yml
```

Test queries can be sent to the exposed CoreDNS service like this:

```
$ ip=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[0].address}')
$ dig @$ip -p 32553 myservicea.foo.org +short
172.18.0.2
$ dig @$ip -p 32553 test.default.foo.org +short
192.168.1.241
```

## Notes regarding Zone Apex and NS server resolution

Due to the fact that there is not nice way to discover NS server's own IP to respond to A queries, as a wokaround, it's possible to pass the name of the LoadBalancer service used to expose the CoreDNS instance as an environment variable `EXTERNAL_SVC`. If not set, the default fallback value of `external-dns.kube-system` will be used to look up the external IP of the CoreDNS service.

## Also see

[Blogpost](https://networkop.co.uk/post/2020-08-k8s-gateway/)