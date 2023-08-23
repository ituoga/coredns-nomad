package nomad

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"
	"github.com/hashicorp/nomad/api"
	nomad "github.com/hashicorp/nomad/api"

	"github.com/miekg/dns"
)

const pluginName = "nomad"

var (
	log         = clog.NewWithPlugin(pluginName)
	defaultTTL  = time.Duration(30 * time.Second).Seconds()
	defaultZone = "service.nomad"
	zone        = defaultZone
)

// Nomad is a plugin that serves records for Nomad services
type Nomad struct {
	Next plugin.Handler

	ttl    uint32
	client *nomad.Client
}

// ServeDNS implements the plugin.Handler interface. This method gets called when example is used
// in a Server.
func (n Nomad) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname := state.Name()
	qtype := state.QType()

	originalQName := qname

	// remove tail for example "service.nomad"
	qname = strings.ReplaceAll(qname, zone, "")
	if len(qname) == 0 {
		return plugin.NextOrFailure(n.Name(), n.Next, ctx, w, r)
	}
	qname = strings.Trim(qname, ".")

	// Split the query name with a `.` as the delimiter and extract namespace and service name.
	// If the query is not for a Nomad service, return.
	qnameSplit := dns.SplitDomainName(qname)
	if len(qname) < 2 {
		return plugin.NextOrFailure(n.Name(), n.Next, ctx, w, r)
	}

	namespace := qnameSplit[1]
	serviceName := qnameSplit[0]

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	m.Compress = true
	m.Rcode = dns.RcodeSuccess
	m.Answer = []dns.RR{}
	m.Extra = []dns.RR{}

	header := dns.RR_Header{
		Name:   state.QName(),
		Rrtype: state.QType(),
		Class:  dns.ClassINET,
		Ttl:    n.ttl,
	}

	// Fetch service registrations for the given service.
	log.Debugf("Looking up record for svc: %s namespace: %s", serviceName, namespace)
	svcRegistrations, _, err := n.client.Services().Get(serviceName, (&api.QueryOptions{Namespace: namespace}))
	if err != nil {
		m.Rcode = dns.RcodeSuccess
		err = w.WriteMsg(m)
		requestFailedCount.WithLabelValues(metrics.WithServer(ctx), namespace).Inc()
		return dns.RcodeServerFailure, err //fmt.Errorf("error fetching service detail: %w", err)
	}

	// If no service registrations are found, ignore this service.
	if len(svcRegistrations) == 0 {
		m.Answer = append(m.Answer, &dns.SOA{
			Hdr: dns.RR_Header{
				Name:   dns.Fqdn(originalQName),
				Rrtype: dns.TypeSOA,
				Class:  dns.ClassINET,
				Ttl:    n.ttl,
			},
			Ns:      "ns1." + originalQName,
			Mbox:    "ns1." + originalQName,
			Serial:  1,
			Refresh: 3600,
			Retry:   600,
			Expire:  604800,
			Minttl:  3600,
		})
		m.Rcode = dns.RcodeNameError
		err = w.WriteMsg(m)
		return dns.RcodeSuccess, err
	}

	// Iterate over all service registrations and add their addresses to the response.
	for _, s := range svcRegistrations {
		// Convert address to an IP and add it to the response.
		addr := net.ParseIP(s.Address)
		if addr == nil {
			m.Rcode = dns.RcodeServerFailure
			err = w.WriteMsg(m)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx), namespace).Inc()
			return dns.RcodeServerFailure, fmt.Errorf("error parsing IP address: %w", err)
		}

		// Check the query type to format the appriopriate response.
		switch qtype {
		case dns.TypeA:
			m.Answer = append(m.Answer, &dns.A{
				Hdr: header,
				A:   addr,
			})
		case dns.TypeAAAA:
			m.Answer = append(m.Answer, &dns.AAAA{
				Hdr:  header,
				AAAA: addr,
			})
		case dns.TypeSRV:
			m.Answer = append(m.Answer, &dns.SRV{
				Hdr:      header,
				Target:   originalQName,
				Port:     uint16(s.Port),
				Priority: 10,
				Weight:   10,
			})
			if addr.To4() == nil {
				m.Extra = append(m.Extra, &dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   originalQName,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET,
						Ttl:    n.ttl,
					},
					AAAA: addr,
				})
			} else {
				m.Extra = append(m.Extra, &dns.A{
					Hdr: dns.RR_Header{
						Name:   originalQName,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    n.ttl,
					},
					A: addr,
				})
			}
		default:
			// log.Errorf("%+v", qtype)
			m.Rcode = dns.RcodeNotImplemented
			err = w.WriteMsg(m)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx), namespace).Inc()
			return dns.RcodeNotImplemented, err
		}
	}

	err = w.WriteMsg(m)
	requestSuccessCount.WithLabelValues(metrics.WithServer(ctx), namespace).Inc()
	return dns.RcodeSuccess, err
}

// Name implements the Handler interface.
func (n Nomad) Name() string { return pluginName }
