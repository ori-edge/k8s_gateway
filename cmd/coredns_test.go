package main

import (
	"testing"

	"github.com/coredns/coredns/core/dnsserver"
)

func TestInit(t *testing.T) {

	var found bool
	for plugin := range dropPlugins {
		for _, included := range dnsserver.Directives {
			if plugin == included {
				t.Errorf("Found unexpected plugin %s", plugin)
			}
			if included == "k8s_gateway" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("'k8s_gateway' plugin is not found in the list")
	}
}
