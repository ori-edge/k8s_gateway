package gateway

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

type lookupFunc func(indexKeys []string) []netip.Addr

type resourceWithIndex struct {
	name   string
	lookup lookupFunc
}

var noop lookupFunc = func([]string) (result []netip.Addr) { return }

var orderedResources = []*resourceWithIndex{
	{
		name:   "HTTPRoute",
		lookup: noop,
	},
	{
		name:   "TLSRoute",
		lookup: noop,
	},
	{
		name:   "GRPCRoute",
		lookup: noop,
	},
	{
		name:   "VirtualServer",
		lookup: noop,
	},
	{
		name:   "Ingress",
		lookup: noop,
	},
	{
		name:   "Service",
		lookup: noop,
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
	prefixes         []netip.Prefix
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

	var isRootZoneQuery bool
	for _, z := range gw.Zones {
		if state.Name() == z { // apex query
			isRootZoneQuery = true
			break
		}
		if dns.IsSubDomain(gw.apex+"."+z, state.Name()) {
			// dns subdomain test for ns. and dns. queries
			ret, err := gw.serveSubApex(state)
			return ret, err
		}
	}

	var addrs []netip.Addr

	// Iterate over supported resources and lookup DNS queries
	// Stop once we've found at least one match
	for _, resource := range gw.Resources {
		addrs = resource.lookup(indexKeys)
		if len(addrs) > 0 {
			break
		}
	}
	log.Debugf("Computed response addresses %v", addrs)

	// Fall through if no host matches
	if len(addrs) == 0 && gw.Fall.Through(qname) {
		return plugin.NextOrFailure(gw.Name(), gw.Next, ctx, w, r)
	}

	m := new(dns.Msg)
	m.SetReply(state.Req)

	var ipv4Addrs []netip.Addr
	var ipv6Addrs []netip.Addr

	for _, addr := range addrs {
		if len(gw.prefixes) > 0 && !matchIpNetPrefix(addr, gw.prefixes) {
			continue
		}
		if addr.Is4() {
			ipv4Addrs = append(ipv4Addrs, addr)
		}
		if addr.Is6() {
			ipv6Addrs = append(ipv6Addrs, addr)
		}
	}

	switch state.QType() {
	case dns.TypeA:

		if len(ipv4Addrs) == 0 {

			if !isRootZoneQuery {
				// No match, return NXDOMAIN
				m.Rcode = dns.RcodeNameError
			}

			m.Ns = []dns.RR{gw.soa(state)}

		} else {

			m.Answer = gw.A(state.Name(), ipv4Addrs)
		}
	case dns.TypeAAAA:

		if len(ipv6Addrs) == 0 {

			if !isRootZoneQuery {
				// No match, return NXDOMAIN
				m.Rcode = dns.RcodeNameError
			}

			// as per rfc4074 #3
			if len(ipv4Addrs) > 0 {
				m.Rcode = dns.RcodeSuccess
			}

			m.Ns = []dns.RR{gw.soa(state)}

		} else {

			m.Answer = gw.AAAA(state.Name(), ipv6Addrs)
		}

	case dns.TypeSOA:

		m.Answer = []dns.RR{gw.soa(state)}

	case dns.TypeNS:

		if isRootZoneQuery {
			m.Answer = gw.nameservers(state)

			addr := gw.ExternalAddrFunc(state)
			for _, rr := range addr {
				rr.Header().Ttl = gw.ttlSOA
				m.Extra = append(m.Extra, rr)
			}
		} else {
			m.Ns = []dns.RR{gw.soa(state)}
		}

	default:
		m.Ns = []dns.RR{gw.soa(state)}
	}

	// Force to true to fix broken behaviour of legacy glibc `getaddrinfo`.
	// See https://github.com/coredns/coredns/pull/3573
	m.Authoritative = true

	if err := w.WriteMsg(m); err != nil {
		log.Errorf("Failed to send a response: %s", err)
	}

	return dns.RcodeSuccess, nil
}

// Name implements the Handler interface.
func (gw *Gateway) Name() string { return thisPlugin }

// A does the A-record lookup in ingress indexer
func (gw *Gateway) A(name string, results []netip.Addr) (records []dns.RR) {
	dup := make(map[string]struct{})
	for _, result := range results {
		if _, ok := dup[result.String()]; !ok {
			dup[result.String()] = struct{}{}
			records = append(records, &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: gw.ttlLow}, A: net.ParseIP(result.String())})
		}
	}
	return records
}

func (gw *Gateway) AAAA(name string, results []netip.Addr) (records []dns.RR) {
	dup := make(map[string]struct{})
	for _, result := range results {
		if _, ok := dup[result.String()]; !ok {
			dup[result.String()] = struct{}{}
			records = append(records, &dns.AAAA{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: gw.ttlLow}, AAAA: net.ParseIP(result.String())})
		}
	}
	return records
}

// SelfAddress returns the address of the local k8s_gateway service
func (gw *Gateway) SelfAddress(state request.Request) (records []dns.RR) {

	var addrs1, addrs2 []netip.Addr
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

	records = append(records, gw.A(gw.apex+"."+state.Zone, addrs1)...)

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

// Returns true if the IP matches at least one of the IP network prefixes
func matchIpNetPrefix(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
