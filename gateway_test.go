package gateway

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/fall"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
)

type FallthroughCase struct {
	test.Case
	FallthroughZones    []string
	FallthroughExpected bool
}

type Fallen struct {
	error
}

func TestLookup(t *testing.T) {
	real := []string{"Ingress", "Service", "HTTPRoute", "VirtualServer"}
	fake := []string{"Pod", "Gateway"}

	for _, resource := range real {
		if found := lookupResource(resource); found == nil {
			t.Errorf("Could not lookup supported resource %s", resource)
		}
	}

	for _, resource := range fake {
		if found := lookupResource(resource); found != nil {
			t.Errorf("Located unsupported resource %s", resource)
		}
	}
}

func TestPlugin(t *testing.T) {

	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.Controller = ctrl
	setupLookupFuncs()

	ctx := context.TODO()
	for i, tc := range tests {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := gw.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d expected no error, got %v", i, err)
			return
		}
		if tc.Error != nil {
			continue
		}

		resp := w.Msg

		if resp == nil {
			t.Fatalf("Test %d, got nil message and no error for %q", i, r.Question[0].Name)
		}
		if err = test.SortAndCheck(resp, tc); err != nil {
			t.Errorf("Test %d failed with error: %v", i, err)
		}
	}
}

func TestPluginFallthrough(t *testing.T) {

	ctrl := &KubeController{hasSynced: true}
	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, Fallen{})
	gw.ExternalAddrFunc = selfAddressTest
	gw.Controller = ctrl
	setupLookupFuncs()

	ctx := context.TODO()
	for i, tc := range testsFallthrough {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		gw.Fall = fall.F{Zones: tc.FallthroughZones}
		_, err := gw.ServeDNS(ctx, w, r)

		if errors.As(err, &Fallen{}) && !tc.FallthroughExpected {
			t.Fatalf("Test %d query resulted unexpectidly in a fall through instead of a response", i)
		}
		if err == nil && tc.FallthroughExpected {
			t.Fatalf("Test %d query resulted unexpectidly in a response instead of a fall through", i)
		}
	}
}

var tests = []test.Case{
	// Existing Service | Test 0
	{
		Qname: "svc1.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc1.ns1.example.com.	60	IN	A	192.0.1.1"),
		},
	},
	// Existing Ingress | Test 1
	{
		Qname: "domain.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.example.com.	60	IN	A	192.0.0.1"),
		},
	},
	// Ingress takes precedence over services | Test 2
	{
		Qname: "svc2.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc2.ns1.example.com.	60	IN	A	192.0.0.2"),
		},
	},
	// Non-existing Service | Test 3
	{
		Qname: "svcX.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	60	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Non-existing Ingress | Test 4
	{
		Qname: "d0main.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	60	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// SOA for the existing domain | Test 5
	{
		Qname: "domain.example.com.", Qtype: dns.TypeSOA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.SOA("example.com.	60	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Service with no public addresses | Test 6
	{
		Qname: "svc3.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	60	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Real service, wrong query type | Test 7
	{
		Qname: "svc3.ns1.example.com.", Qtype: dns.TypeCNAME, Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	60	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Ingress FQDN == zone | Test 8
	{
		Qname: "example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("example.com.	60	IN	A	192.0.0.3"),
		},
	},
	// Existing Ingress with a mix of lower and upper case letters | Test 9
	{
		Qname: "dOmAiN.eXamPLe.cOm.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.example.com.	60	IN	A	192.0.0.1"),
		},
	},
	// Existing Service with a mix of lower and upper case letters | Test 10
	{
		Qname: "svC1.Ns1.exAmplE.Com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc1.ns1.example.com.	60	IN	A	192.0.1.1"),
		},
	},
	// Existing VirtualServer | Test 11
	{
		Qname: "vs1.example.com", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("vs1.example.com.	60	IN	A	192.0.3.1"),
		},
	},
	// VirtualServer lookup priority over Ingress | Test 12
	{
		Qname: "shadow-vs.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("shadow-vs.example.com.	60	IN	A	192.0.3.5"),
		},
	},
	// basic gateway API lookup | Test 13
	{
		Qname: "domain.gw.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.gw.example.com.	60	IN	A	192.0.2.1"),
		},
	},
	// gateway API lookup priority over Ingress and VirtualServers | Test 14
	{
		Qname: "shadow.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("shadow.example.com.	60	IN	A	192.0.2.4"),
		},
	},
}

var testsFallthrough = []FallthroughCase{
	// Match found, fallthrough enabled | Test 0
	{
		Case:             test.Case{Qname: "example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"."}, FallthroughExpected: false,
	},
	// No match found, fallthrough enabled | Test 1
	{
		Case:             test.Case{Qname: "non-existent.example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"."}, FallthroughExpected: true,
	},
	// Match found, fallthrough for different zone | Test 2
	{
		Case:             test.Case{Qname: "example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"not-example.com."}, FallthroughExpected: false,
	},
	// No match found, fallthrough for different zone | Test 3
	{
		Case:             test.Case{Qname: "non-existent.example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"not-example.com."}, FallthroughExpected: false,
	},
	// No fallthrough on gw apex | Test 4
	{
		Case:             test.Case{Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeA},
		FallthroughZones: []string{"."}, FallthroughExpected: false,
	},
}

var testServiceIndexes = map[string][]netip.Addr{
	"svc1.ns1": {netip.MustParseAddr("192.0.1.1")},
	"svc2.ns1": {netip.MustParseAddr("192.0.1.2")},
	"svc3.ns1": {},
}

func testServiceLookup(keys []string) (results []netip.Addr) {
	for _, key := range keys {
		results = append(results, testServiceIndexes[strings.ToLower(key)]...)
	}
	return results
}

var testIngressIndexes = map[string][]netip.Addr{
	"domain.example.com":    {netip.MustParseAddr("192.0.0.1")},
	"svc2.ns1.example.com":  {netip.MustParseAddr("192.0.0.2")},
	"example.com":           {netip.MustParseAddr("192.0.0.3")},
	"shadow.example.com":    {netip.MustParseAddr("192.0.0.4")},
	"shadow-vs.example.com": {netip.MustParseAddr("192.0.0.5")},
}

func testIngressLookup(keys []string) (results []netip.Addr) {
	for _, key := range keys {
		results = append(results, testIngressIndexes[strings.ToLower(key)]...)
	}
	return results
}

var testVirtualServerIndexes = map[string][]netip.Addr{
	"vs1.example.com":       {netip.MustParseAddr("192.0.3.1")},
	"shadow.example.com":    {netip.MustParseAddr("192.0.3.4")},
	"shadow-vs.example.com": {netip.MustParseAddr("192.0.3.5")},
}

func testVirtualServerLookup(keys []string) (results []netip.Addr) {
	for _, key := range keys {
		results = append(results, testVirtualServerIndexes[strings.ToLower(key)]...)
	}
	return results
}

var testHTTPRouteIndexes = map[string][]netip.Addr{
	"domain.gw.example.com": {netip.MustParseAddr("192.0.2.1")},
	"shadow.example.com":    {netip.MustParseAddr("192.0.2.4")},
}

func testHTTPRouteLookup(keys []string) (results []netip.Addr) {
	for _, key := range keys {
		results = append(results, testHTTPRouteIndexes[strings.ToLower(key)]...)
	}
	return results
}

func setupLookupFuncs() {
	if resource := lookupResource("Ingress"); resource != nil {
		resource.lookup = testIngressLookup
	}
	if resource := lookupResource("Service"); resource != nil {
		resource.lookup = testServiceLookup
	}
	if resource := lookupResource("VirtualServer"); resource != nil {
		resource.lookup = testVirtualServerLookup
	}
	if resource := lookupResource("HTTPRoute"); resource != nil {
		resource.lookup = testHTTPRouteLookup
	}
}
