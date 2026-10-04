package discovery

import (
	"context"
	"time"

	"github.com/brutella/dnssd"
)

// EmberServer is an Ember server found by browsing _ember._tcp (what
// Advertise announces): headless producers use it to find their server.
type EmberServer struct {
	Name    string // mDNS instance name, "Ember" (or "Ember (2)" after a conflict)
	Host    string // advertising host, e.g. "unraid.local."
	URL     string // http://<ipv4>:<port>
	Version string // TXT "version"
}

// BrowseEmber browses the LAN for timeout and returns every resolved Ember
// server, one per instance name.
func BrowseEmber(ctx context.Context, timeout time.Duration) ([]EmberServer, error) {
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	seen := map[string]EmberServer{}
	var order []string
	err := dnssd.LookupType(bctx, emberServiceType+".local.", func(e dnssd.BrowseEntry) {
		s, ok := emberServerFrom(e)
		if !ok {
			return
		}
		if _, dup := seen[s.Name]; !dup {
			order = append(order, s.Name)
		}
		seen[s.Name] = s
	}, func(dnssd.BrowseEntry) {})
	if err != nil && bctx.Err() == nil {
		return nil, err
	}
	out := make([]EmberServer, 0, len(order))
	for _, n := range order {
		out = append(out, seen[n])
	}
	return out, nil
}

func emberServerFrom(e dnssd.BrowseEntry) (EmberServer, bool) {
	if e.Port == 0 {
		return EmberServer{}, false
	}
	url := baseURLFor(e.IPs, e.Port)
	if url == "" {
		return EmberServer{}, false
	}
	return EmberServer{Name: e.Name, Host: e.Host, URL: url, Version: e.Text["version"]}, true
}
