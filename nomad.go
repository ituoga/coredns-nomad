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

	ttl uint32

	clients []*api.Client // List of clients
	current int
}

func (n *Nomad) Name() string {
	return pluginName
}

func (n Nomad) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname, originalQName := processQName(state.Name())

	namespace, serviceName, err := extractNamespaceAndService(qname)
	if err != nil {
		return plugin.NextOrFailure(n.Name(), n.Next, ctx, w, r)
	}

	m, header := initializeMessage(state, n.ttl)

	svcRegistrations, _, err := fetchServiceRegistrations(n, serviceName, namespace)
	if err != nil {
		log.Warning(err)
		return handleServiceLookupError(w, m, ctx, namespace)
	}

	if len(svcRegistrations) == 0 {
		return handleResponseError(w, m, originalQName, n.ttl, ctx, namespace, err)
	}

	if err := addServiceResponses(m, svcRegistrations, header, state.QType(), originalQName, n.ttl); err != nil {
		return handleResponseError(w, m, originalQName, n.ttl, ctx, namespace, err)
	}

	err = w.WriteMsg(m)
	requestSuccessCount.WithLabelValues(metrics.WithServer(ctx), namespace).Inc()
	return dns.RcodeSuccess, err
}

func processQName(qname string) (string, string) {
	originalQName := qname
	qname = strings.ReplaceAll(qname, zone, "")
	qname = strings.Trim(qname, ".")
	return qname, originalQName
}

func extractNamespaceAndService(qname string) (string, string, error) {
	qnameSplit := dns.SplitDomainName(qname)
	if len(qnameSplit) < 2 {
		return "", "", fmt.Errorf("invalid query name")
	}
	return qnameSplit[1], qnameSplit[0], nil
}

func initializeMessage(state request.Request, ttl uint32) (*dns.Msg, dns.RR_Header) {
	m := new(dns.Msg)
	m.SetReply(state.Req)
	m.Authoritative, m.Compress, m.Rcode = true, true, dns.RcodeSuccess

	header := dns.RR_Header{
		Name:   state.QName(),
		Rrtype: state.QType(),
		Class:  dns.ClassINET,
		Ttl:    ttl,
	}

	return m, header
}

func fetchServiceRegistrations(n Nomad, serviceName, namespace string) ([]*api.ServiceRegistration, *api.QueryMeta, error) {
	log.Debugf("Looking up record for svc: %s namespace: %s", serviceName, namespace)
	nc := n.getClient()
	if nc == nil {
		return nil, nil, fmt.Errorf("no Nomad client available")
	}
	return nc.Services().Get(serviceName, (&api.QueryOptions{Namespace: namespace}))
}

func handleServiceLookupError(w dns.ResponseWriter, m *dns.Msg, ctx context.Context, namespace string) (int, error) {
	m.Rcode = dns.RcodeSuccess
	err := w.WriteMsg(m)
	requestFailedCount.WithLabelValues(metrics.WithServer(ctx), namespace).Inc()
	return dns.RcodeServerFailure, err
}

func addServiceResponses(m *dns.Msg, svcRegistrations []*api.ServiceRegistration, header dns.RR_Header, qtype uint16, originalQName string, ttl uint32) error {
	for _, s := range svcRegistrations {
		addr := net.ParseIP(s.Address)
		if addr == nil {
			return fmt.Errorf("error parsing IP address")
		}

		switch qtype {
		case dns.TypeA:
			addARecord(m, header, addr)
		case dns.TypeAAAA:
			addAAAARecord(m, header, addr)
		case dns.TypeSRV:
			err := addSRVRecord(m, s, header, originalQName, addr, ttl)
			if err != nil {
				return err
			}
		default:
			m.Rcode = dns.RcodeNotImplemented
			return fmt.Errorf("query type not implemented")
		}
	}
	return nil
}

func handleResponseError(w dns.ResponseWriter, m *dns.Msg, originalQName string, ttl uint32, ctx context.Context, namespace string, err error) (int, error) {
	m.Rcode = dns.RcodeNameError
	m.Answer = append(m.Answer, createSOARecord(originalQName, ttl))

	if writeErr := w.WriteMsg(m); writeErr != nil {
		return dns.RcodeServerFailure, fmt.Errorf("write message error: %w", writeErr)
	}

	requestFailedCount.WithLabelValues(metrics.WithServer(ctx), namespace).Inc()

	return dns.RcodeSuccess, err
}
