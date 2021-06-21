package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

type lookupFunc func(indexKeys []string) []net.IP

type resourceWithIndex struct {
	name   string
	lookup lookupFunc
}

var orderedResources = []*resourceWithIndex{
	{
		name: "Ingress",
	},
	{
		name: "Service",
	},
}

var (
	ttlDefault        = uint32(60)
	ttlSOA            = uint32(60)
	defaultApex       = "dns1.kube-system"
	defaultHostmaster = "hostmaster"
	defaultSecondNS   = ""
)

// Gateway stores all runtime configuration of a plugin
type Gateway struct {
	Next             plugin.Handler
	Zones            []string
	Resources        []*resourceWithIndex
	ttlLow           uint32
	ttlSOA           uint32
	Controller       *KubeController
	apex             string
	hostmaster       string
	secondNS         string
	configFile       string
	configContext    string
	ExternalAddrFunc func(request.Request) []dns.RR

	Fall fall.F
}

func newGateway() *Gateway {
	return &Gateway{
		Resources:  orderedResources,
		ttlLow:     ttlDefault,
		ttlSOA:     ttlSOA,
		apex:       defaultApex,
		secondNS:   defaultSecondNS,
		hostmaster: defaultHostmaster,
	}
}

func lookupResource(resource string) *resourceWithIndex {

	for _, r := range orderedResources {
		if r.name == resource {
			return r
		}
	}
	return nil
}

func (gw *Gateway) updateResources(newResources []string) {

	gw.Resources = []*resourceWithIndex{}

	for _, name := range newResources {
		if resource := lookupResource(name); resource != nil {
			gw.Resources = append(gw.Resources, resource)
		}
	}
}

// ServeDNS implements the plugin.Handle interface.
func (gw *Gateway) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	//log.Infof("Incoming query %s", state.QName())

	qname := state.QName()
	zone := plugin.Zones(gw.Zones).Matches(qname)
	if zone == "" {
		log.Debugf("Request %s has not matched any zones %v", qname, gw.Zones)
		return plugin.NextOrFailure(gw.Name(), gw.Next, ctx, w, r)
	}
	zone = qname[len(qname)-len(zone):] // maintain case of original query
	state.Zone = zone

	// Indexer cache can be built from `name.namespace` without zone
	zonelessQuery := stripDomain(qname, zone)

	// Computing keys to look up in cache
	var indexKeys []string
	strippedQName := stripClosingDot(state.QName())
	if len(zonelessQuery) != 0 && zonelessQuery != strippedQName {
		indexKeys = []string{strippedQName, zonelessQuery}
	} else {
		indexKeys = []string{strippedQName}
	}
	log.Debugf("Computed Index Keys %v", indexKeys)

	if !gw.Controller.HasSynced() {
		// TODO maybe there's a better way to do this? e.g. return an error back to the client?
		return dns.RcodeServerFailure, plugin.Error(thisPlugin, fmt.Errorf("Could not sync required resources"))
	}

	for _, z := range gw.Zones {
		if state.Name() == z { // apex query
			ret, err := gw.serveApex(state)
			return ret, err
		}
		if dns.IsSubDomain(gw.apex+"."+z, state.Name()) {
			// dns subdomain test for ns. and dns. queries
			ret, err := gw.serveSubApex(state)
			return ret, err
		}
	}

	var addrs []net.IP

	// Iterate over supported resources and lookup DNS queries
	// Stop once we've found at least one match
	for _, resource := range gw.Resources {
		addrs = resource.lookup(indexKeys)
		if len(addrs) > 0 {
			break
		}
	}
	log.Debugf("Computed response addresses %v", addrs)

	m := new(dns.Msg)
	m.SetReply(state.Req)

	// If there's no match, fall through or return NXDOMAIN
	if len(addrs) == 0 {
		if gw.Fall.Through(qname) {
			return plugin.NextOrFailure(gw.Name(), gw.Next, ctx, w, r)
		}

		m.Rcode = dns.RcodeNameError
		m.Ns = []dns.RR{gw.soa(state)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to send a response: %s", err)
		}
		return 0, nil
	}

	switch state.QType() {
	case dns.TypeA:
		m.Answer = gw.A(state.Name(), addrs)
		// Force to true to fix broken behaviour of legacy glibc `getaddrinfo`.
		// See https://github.com/coredns/coredns/pull/3573
		m.Authoritative = true
	default:
		m.Ns = []dns.RR{gw.soa(state)}
	}

	// If there's no match, fall through or return the SOA
	if len(m.Answer) == 0 {
		if gw.Fall.Through(qname) {
			return plugin.NextOrFailure(gw.Name(), gw.Next, ctx, w, r)
		}

		m.Ns = []dns.RR{gw.soa(state)}
	}

	if err := w.WriteMsg(m); err != nil {
		log.Errorf("Failed to send a response: %s", err)
	}

	return dns.RcodeSuccess, nil
}

// Name implements the Handler interface.
func (gw *Gateway) Name() string { return thisPlugin }

// A does the A-record lookup in ingress indexer
func (gw *Gateway) A(name string, results []net.IP) (records []dns.RR) {
	dup := make(map[string]struct{})
	for _, result := range results {
		if _, ok := dup[result.String()]; !ok {
			dup[result.String()] = struct{}{}
			records = append(records, &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: gw.ttlLow}, A: result})
		}
	}
	return records
}

// SelfAddress returns the address of the local k8s_gateway service
func (gw *Gateway) SelfAddress(state request.Request) (records []dns.RR) {

	var addrs1, addrs2 []net.IP
	for _, resource := range gw.Resources {
		results := resource.lookup([]string{gw.apex})
		if len(results) > 0 {
			addrs1 = append(addrs1, results...)
		}
		results = resource.lookup([]string{gw.secondNS})
		if len(results) > 0 {
			addrs2 = append(addrs2, results...)
		}
	}

	records = append(records, gw.A(state.Name(), addrs1)...)

	if state.QType() == dns.TypeNS {
		records = append(records, gw.A(gw.secondNS+"."+state.Zone, addrs2)...)
	}

	return records
	//return records
}

// Strips the zone from FQDN and return a hostname
func stripDomain(qname, zone string) string {
	hostname := qname[:len(qname)-len(zone)]
	return stripClosingDot(hostname)
}

// Strips the closing dot unless it's "."
func stripClosingDot(s string) string {
	if len(s) > 1 {
		return strings.TrimSuffix(s, ".")
	}
	return s
}
