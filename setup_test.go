package gateway

import (
	"testing"

	"github.com/coredns/caddy"
)

func TestSetup(t *testing.T) {
	tests := []struct {
		input         string
		shouldErr     bool
		expectedZone  string
		expectedZones int
	}{
		{`k8s_gateway`, false, "", 1},
		{`k8s_gateway example.org`, false, "example.org.", 1},
		{`k8s_gateway example.org sub.example.org`, false, "sub.example.org.", 2},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		gw, err := parse(c)

		if test.shouldErr && err == nil {
			t.Errorf("Test %d: Expected error but found %s for input %s", i, err, test.input)
		}

		if err != nil {
			if !test.shouldErr {
				t.Errorf("Test %d: Expected no error but found one for input %s. Error was: %v", i, test.input, err)
			}
		}

		if !test.shouldErr && test.expectedZone != "" {
			if test.expectedZones != len(gw.Zones) {
				t.Errorf("Test %d, expected zone %q for input %s, got: %q", i, test.expectedZone, test.input, gw.Zones[0])
			}
		}
	}
}
