package gateway

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
)

func TestLookup(t *testing.T) {
	real := []string{"Ingress", "Service"}
	fake := []string{"Gateway", "Pod"}

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

func TestGateway(t *testing.T) {

	ctrl := &KubeController{hasSynced: true}

	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.Controller = ctrl
	setupTestLookupFuncs()

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

var tests = []test.Case{
	// Existing Service
	{
		Qname: "svc1.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc1.ns1.example.com.	60	IN	A	192.0.1.1"),
		},
	},
	// Existing Ingress
	{
		Qname: "domain.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.example.com.	60	IN	A	192.0.0.1"),
		},
	},
	// Ingress takes precedence over services
	{
		Qname: "svc2.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc2.ns1.example.com.	60	IN	A	192.0.0.2"),
		},
	},
	// Non-existing Service
	{
		Qname: "svcX.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	ns1.dns.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Non-existing Ingress
	{
		Qname: "d0main.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	ns1.dns.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// SOA for the existing domain
	{
		Qname: "domain.example.com.", Qtype: dns.TypeSOA, Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	ns1.dns.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Service with no public addresses
	{
		Qname: "svc3.ns1.example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	ns1.dns.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Real service, wrong query type
	{
		Qname: "svc3.ns1.example.com.", Qtype: dns.TypeAAAA, Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	ns1.dns.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Ingress FQDN == zone
	{
		Qname: "example.com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	ns1.dns.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	// Existing Ingress with a mix of lower and upper case letters
	{
		Qname: "dOmAiN.eXamPLe.cOm.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("domain.example.com.	60	IN	A	192.0.0.1"),
		},
	},
	// Existing Service with a mix of lower and upper case letters
	{
		Qname: "svC1.Ns1.exAmplE.Com.", Qtype: dns.TypeA, Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("svc1.ns1.example.com.	60	IN	A	192.0.1.1"),
		},
	},
}

var testServiceIndexes = map[string][]net.IP{
	"svc1.ns1": {net.ParseIP("192.0.1.1")},
	"svc2.ns1": {net.ParseIP("192.0.1.2")},
	"svc3.ns1": {},
}

func testServiceLookup(keys []string) (results []net.IP) {
	for _, key := range keys {
		results = append(results, testServiceIndexes[strings.ToLower(key)]...)
	}
	return results
}

var testIngressIndexes = map[string][]net.IP{
	"domain.example.com":   {net.ParseIP("192.0.0.1")},
	"svc2.ns1.example.com": {net.ParseIP("192.0.0.2")},
	"example.com":          {net.ParseIP("192.0.0.3")},
}

func testIngressLookup(keys []string) (results []net.IP) {
	for _, key := range keys {
		results = append(results, testIngressIndexes[strings.ToLower(key)]...)
	}
	return results
}

func setupTestLookupFuncs() {
	if resource := lookupResource("Ingress"); resource != nil {
		resource.lookup = testIngressLookup
	}
	if resource := lookupResource("Service"); resource != nil {
		resource.lookup = testServiceLookup
	}
}
