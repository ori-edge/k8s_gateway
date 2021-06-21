package gateway

import (
	"github.com/coredns/coredns/plugin/pkg/dnsutil"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"
)

// serveApex serves request that hit the zone' apex. A reply is written back to the client.
func (gw *Gateway) serveApex(state request.Request) (int, error) {
	m := new(dns.Msg)
	m.SetReply(state.Req)
	switch state.QType() {
	case dns.TypeSOA:
		// Force to true to fix broken behaviour of legacy glibc `getaddrinfo`.
		// See https://github.com/coredns/coredns/pull/3573
		m.Authoritative = true
		m.Answer = []dns.RR{gw.soa(state)}
	case dns.TypeNS:
		m.Answer = gw.nameservers(state)

		addr := gw.ExternalAddrFunc(state)
		for _, rr := range addr {
			rr.Header().Ttl = gw.ttlSOA
			m.Extra = append(m.Extra, rr)
		}
	default:
		m.Ns = []dns.RR{gw.soa(state)}
	}

	if err := state.W.WriteMsg(m); err != nil {
		log.Errorf("Failed to send a response: %s", err)
	}
	return 0, nil
}

// serveSubApex serves requests that hit the zones fake 'dns' subdomain where our nameservers live.
func (gw *Gateway) serveSubApex(state request.Request) (int, error) {
	base, _ := dnsutil.TrimZone(state.Name(), state.Zone)

	m := new(dns.Msg)
	m.SetReply(state.Req)

	// base is gw.apex, if it's longer return nxdomain
	switch labels := dns.CountLabel(base); labels {
	default:
		m.SetRcode(m, dns.RcodeNameError)
		m.Ns = []dns.RR{gw.soa(state)}
		if err := state.W.WriteMsg(m); err != nil {
			log.Errorf("Failed to send a response: %s", err)
		}
		return 0, nil
	case 2:
		if base != gw.apex {
			// nxdomain
			m.SetRcode(m, dns.RcodeNameError)
			m.Ns = []dns.RR{gw.soa(state)}
			if err := state.W.WriteMsg(m); err != nil {
				log.Errorf("Failed to send a response: %s", err)
			}
			return 0, nil
		}

		addr := gw.ExternalAddrFunc(state)
		for _, rr := range addr {
			rr.Header().Ttl = gw.ttlSOA
			rr.Header().Name = state.QName()
			switch state.QType() {
			case dns.TypeA:
				if rr.Header().Rrtype == dns.TypeA {
					m.Answer = append(m.Answer, rr)
				}
			case dns.TypeAAAA:
				if rr.Header().Rrtype == dns.TypeAAAA {
					m.Answer = append(m.Answer, rr)
				}
			}
		}

		if len(m.Answer) == 0 {
			m.Ns = []dns.RR{gw.soa(state)}
		}

		if err := state.W.WriteMsg(m); err != nil {
			log.Errorf("Failed to send a response: %s", err)
		}
		return 0, nil

	}
}

func (gw *Gateway) soa(state request.Request) *dns.SOA {
	header := dns.RR_Header{Name: state.Zone, Rrtype: dns.TypeSOA, Ttl: gw.ttlSOA, Class: dns.ClassINET}

	soa := &dns.SOA{Hdr: header,
		Mbox:    dnsutil.Join(gw.hostmaster, gw.apex, state.Zone),
		Ns:      dnsutil.Join(gw.apex, state.Zone),
		Serial:  12345, // Also dynamic?
		Refresh: 7200,
		Retry:   1800,
		Expire:  86400,
		Minttl:  gw.ttlSOA,
	}
	return soa
}

func (gw *Gateway) nameservers(state request.Request) (result []dns.RR) {
	primaryNS := gw.ns1(state)
	result = append(result, primaryNS)

	secondaryNS := gw.ns2(state)
	if secondaryNS != nil {
		result = append(result, secondaryNS)
	}

	return result
}

func (gw *Gateway) ns1(state request.Request) *dns.NS {
	header := dns.RR_Header{Name: state.Zone, Rrtype: dns.TypeNS, Ttl: gw.ttlSOA, Class: dns.ClassINET}
	ns := &dns.NS{Hdr: header, Ns: dnsutil.Join(gw.apex, state.Zone)}

	return ns
}

func (gw *Gateway) ns2(state request.Request) *dns.NS {
	if gw.secondNS == "" { // If second NS is undefined, return nothing
		return nil
	}
	header := dns.RR_Header{Name: state.Zone, Rrtype: dns.TypeNS, Ttl: gw.ttlSOA, Class: dns.ClassINET}
	ns := &dns.NS{Hdr: header, Ns: dnsutil.Join(gw.secondNS, state.Zone)}

	return ns
}
