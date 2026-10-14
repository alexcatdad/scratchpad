package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type proxyClients map[netip.Addr]struct{}

func clientAddress(value string) (netip.Addr, bool) {
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func trustedProxyClients(values []string) proxyClients {
	trusted := proxyClients{}
	for _, value := range values {
		if address, ok := clientAddress(strings.TrimSpace(value)); ok {
			trusted[address] = struct{}{}
		}
	}
	return trusted
}

// Only the socket peer and an explicitly trusted suffix of proxy hops can
// choose an admission identity. A caller-controlled prefix cannot replace it.
func (s *SSH) requestClient(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	client, ok := clientAddress(peer)
	if !ok {
		return "unknown"
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if _, trusted := s.trustedProxies[client]; !trusted || forwarded == "" || len(forwarded) > 8192 {
		return client.String()
	}
	hops := strings.Split(forwarded, ",")
	if len(hops) > 32 {
		return client.String()
	}
	for len(hops) > 0 {
		if _, trusted := s.trustedProxies[client]; !trusted {
			break
		}
		hop, ok := clientAddress(strings.TrimSpace(hops[len(hops)-1]))
		if !ok {
			break
		}
		client = hop
		hops = hops[:len(hops)-1]
	}
	return client.String()
}
