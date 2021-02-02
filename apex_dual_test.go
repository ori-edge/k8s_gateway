package gateway

import (
	"context"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"
)

func TestDualNS(t *testing.T) {

	ctrl := &KubeController{hasSynced: true}
	gw := newGateway()
	gw.Zones = []string{"example.com."}
	gw.Next = test.NextHandler(dns.RcodeSuccess, nil)
	gw.Controller = ctrl
	gw.ExternalAddrFunc = selfDualAddressTest
	gw.secondNS = "dns2.kube-system"

	ctx := context.TODO()
	for i, tc := range testsDualNS {
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
		if err := test.SortAndCheck(resp, tc); err != nil {
			t.Errorf("Test number #%d: %+v", i, err)
		}
	}
}

var testsDualNS = []test.Case{
	{
		Qname: "example.com.", Qtype: dns.TypeSOA,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	{
		Qname: "example.com.", Qtype: dns.TypeNS,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.NS("example.com.	3600	IN	NS	dns1.kube-system.example.com."),
			test.NS("example.com.	3600	IN	NS	dns2.kube-system.example.com."),
		},
		Extra: []dns.RR{
			test.A("dns1.kube-system.example.com.	3600	IN	A	127.0.0.1"),
			test.A("dns2.kube-system.example.com.	3600	IN	A	127.0.0.2"),
		},
	},
	{
		Qname: "example.com.", Qtype: dns.TypeSRV,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	{
		Qname: "example.com.", Qtype: dns.TypeA,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeSRV,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeNS,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeSOA,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeAAAA,
		Rcode: dns.RcodeSuccess,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
	{
		Qname: "dns1.kube-system.example.com.", Qtype: dns.TypeA,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("dns1.kube-system.example.com.	3600	IN	A	127.0.0.1"),
		},
	},
	{
		Qname: "foo.dns1.kube-system.example.com.", Qtype: dns.TypeA,
		Rcode: dns.RcodeNameError,
		Ns: []dns.RR{
			test.SOA("example.com.	3600	IN	SOA	dns1.kube-system.example.com. hostmaster.example.com. 1499347823 7200 1800 86400 5"),
		},
	},
}

func selfDualAddressTest(state request.Request) (result []dns.RR) {
	result = append(result, test.A("dns1.kube-system.example.com. IN A 127.0.0.1"))
	if state.QType() == dns.TypeNS {
		result = append(result, test.A("dns2.kube-system.example.com. IN A 127.0.0.2"))
	}
	return result
}
