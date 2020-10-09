package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

const defaultSvc = "external-dns.kube-system"

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
	defaultTTL        = uint32(5)
	defaultApex       = "dns"
	defaultHostmaster = "hostmaster"
)

// Gateway stores all runtime configuration of a plugin
type Gateway struct {
	Next       plugin.Handler
	Zones      []string
	Resources  []*resourceWithIndex
	ttl        uint32
	Controller *KubeController
	apex       string
	hostmaster string
}

func newGateway() *Gateway {
	return &Gateway{
		Resources:  orderedResources,
		ttl:        defaultTTL,
		apex:       defaultApex,
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

	if len(addrs) == 0 {
		m.Rcode = dns.RcodeNameError
		m.Ns = []dns.RR{gw.soa(state)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to send a response: %s", err)
		}
		return 0, nil
	}

	switch state.QType() {
	case dns.TypeA:
		m.Answer = gw.A(state, addrs)
	default:
		m.Ns = []dns.RR{gw.soa(state)}
	}

	if len(m.Answer) == 0 {
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
func (gw *Gateway) A(state request.Request, results []net.IP) (records []dns.RR) {
	dup := make(map[string]struct{})
	for _, result := range results {
		if _, ok := dup[result.String()]; !ok {
			dup[result.String()] = struct{}{}
			records = append(records, &dns.A{Hdr: dns.RR_Header{Name: state.Name(), Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: gw.ttl}, A: result})
		}
	}
	return records
}

func (gw *Gateway) selfAddress(state request.Request) (records []dns.RR) {
	// TODO: need to do self-index lookup for that i need
	// a) my own namespace - easy
	// b) my own serviceName - CoreDNS/k does that via localIP->Endpoint->Service
	// I don't really want to list Endpoints just for that so will fix that later

	// As a workaround I'm reading an env variable (with a default)
	//// TODO: update docs to surface this knob
	//index := os.Getenv("EXTERNAL_SVC")
	//if index == "" {
	//	index = defaultSvc
	//}
	//
	//var addrs []net.IP
	//for _, resource := range gw.Resources {
	//	addrs = resource.lookup([]string{index})
	//	if len(addrs) > 0 {
	//		break
	//	}
	//}
	//
	//m := new(dns.Msg)
	//m.SetReply(state.Req)
	//return gw.A(state, addrs)
	return records
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
