package main

import (
	"fmt"

	_ "github.com/coredns/coredns/core/plugin"
	_ "github.com/ori-edge/k8s_gateway"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"
)

var dropPlugins = map[string]bool{
	"kubernetes":   true,
	"k8s_external": true,
}

const pluginVersion = "0.4.0"

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
	// extend CoreDNS version with plugin details
	caddy.AppVersion = fmt.Sprintf("%s+k8s_gateway-%s", coremain.CoreVersion, pluginVersion)
	coremain.Run()
}
