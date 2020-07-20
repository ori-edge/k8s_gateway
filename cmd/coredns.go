package main

import (
	_ "github.com/coredns/coredns/core/plugin"
	_ "github.com/ori-edge/k8s_gateway"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"
)

var dropPlugins = map[string]bool{
	"kubernetes":   true,
	"k8s_external": true,
}

func init() {
	var directives []string
	var alreadyAdded bool

	for _, name := range dnsserver.Directives {
		if dropPlugins[name] {
			if !alreadyAdded {
				directives = append(directives, "k8s_gateway")
				alreadyAdded = true
			}
			continue
		}
		directives = append(directives, name)
	}

	dnsserver.Directives = directives

}

func main() {
	coremain.Run()
}
