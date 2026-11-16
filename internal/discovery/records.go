package discovery

import (
	"net"
	"slices"
	"strings"

	"github.com/brutella/dnssd"
	"github.com/miekg/dns"
)

type ifaceIP struct {
	iface string
	ip    net.IP
}

type mdnsRecords struct {
	service   string
	instances []string
	srv       map[string][]*dns.SRV
	txt       map[string][]string
	addrs     map[string][]ifaceIP
}

func newMDNSRecords(service string) *mdnsRecords {
	return &mdnsRecords{
		service: service,
		srv:     map[string][]*dns.SRV{},
		txt:     map[string][]string{},
		addrs:   map[string][]ifaceIP{},
	}
}

func collectRecords(service string, msgs []*dns.Msg) *mdnsRecords {
	r := newMDNSRecords(service)
	for _, m := range msgs {
		r.add(m, "")
	}
	return r
}

func (r *mdnsRecords) add(m *dns.Msg, iface string) {
	all := slices.Concat(m.Answer, m.Ns, m.Extra)
	for _, rr := range all {
		switch v := rr.(type) {
		case *dns.PTR:
			if !strings.EqualFold(v.Hdr.Name, r.service) {
				continue
			}
			if v.Hdr.Ttl == 0 {
				r.forget(v.Ptr)
			} else {
				r.instance(v.Ptr)
			}
		case *dns.SRV:
			if !r.under(v.Hdr.Name) {
				continue
			}
			key := strings.ToLower(v.Hdr.Name)
			if v.Hdr.Ttl == 0 {
				r.srv[key] = slices.DeleteFunc(r.srv[key], func(o *dns.SRV) bool { return sameSRV(o, v) })
				continue
			}
			r.instance(v.Hdr.Name)
			if !slices.ContainsFunc(r.srv[key], func(o *dns.SRV) bool { return sameSRV(o, v) }) {
				r.srv[key] = append(r.srv[key], v)
			}
		}
	}
	for _, rr := range all {
		name := strings.ToLower(rr.Header().Name)
		switch v := rr.(type) {
		case *dns.TXT:
			if r.known(name) {
				r.txt[name] = v.Txt
			}
		case *dns.A:
			r.addAddr(name, iface, v.A)
		case *dns.AAAA:
			r.addAddr(name, iface, v.AAAA)
		}
	}
}

func sameSRV(a, b *dns.SRV) bool {
	return strings.EqualFold(a.Target, b.Target) && a.Port == b.Port
}

func (r *mdnsRecords) under(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), "."+strings.ToLower(r.service))
}

func (r *mdnsRecords) known(key string) bool {
	return slices.ContainsFunc(r.instances, func(i string) bool { return strings.ToLower(i) == key })
}

func (r *mdnsRecords) instance(name string) {
	if !r.known(strings.ToLower(name)) {
		r.instances = append(r.instances, name)
	}
}

func (r *mdnsRecords) forget(name string) {
	key := strings.ToLower(name)
	r.instances = slices.DeleteFunc(r.instances, func(i string) bool { return strings.ToLower(i) == key })
	delete(r.srv, key)
	delete(r.txt, key)
}

func (r *mdnsRecords) addAddr(host, iface string, ip net.IP) {
	if !r.isTarget(host) {
		return
	}
	if slices.ContainsFunc(r.addrs[host], func(a ifaceIP) bool { return a.iface == iface && a.ip.Equal(ip) }) {
		return
	}
	r.addrs[host] = append(r.addrs[host], ifaceIP{iface: iface, ip: ip})
}

func (r *mdnsRecords) isTarget(host string) bool {
	for _, list := range r.srv {
		for _, s := range list {
			if strings.EqualFold(s.Target, host) {
				return true
			}
		}
	}
	return false
}

func (r *mdnsRecords) ipv4s(host string) []net.IP {
	var out []net.IP
	for _, a := range r.addrs[strings.ToLower(host)] {
		if a.ip.To4() != nil && !slices.ContainsFunc(out, a.ip.Equal) {
			out = append(out, a.ip)
		}
	}
	return out
}

func (r *mdnsRecords) entries() []dnssd.BrowseEntry {
	var out []dnssd.BrowseEntry
	for _, inst := range r.instances {
		key := strings.ToLower(inst)
		text := map[string]string{}
		for _, kv := range r.txt[key] {
			if k, v, ok := strings.Cut(kv, "="); ok {
				text[k] = v
			}
		}
		for _, s := range r.srv[key] {
			var ifaces []string
			byIface := map[string][]net.IP{}
			for _, a := range r.addrs[strings.ToLower(s.Target)] {
				if _, ok := byIface[a.iface]; !ok {
					ifaces = append(ifaces, a.iface)
				}
				byIface[a.iface] = append(byIface[a.iface], a.ip)
			}
			host, _, _ := strings.Cut(s.Target, ".")
			for _, iface := range ifaces {
				out = append(out, dnssd.BrowseEntry{
					Name:      instanceLabel(inst, r.service),
					Host:      host,
					Port:      int(s.Port),
					IfaceName: iface,
					IPs:       byIface[iface],
					Text:      text,
				})
			}
		}
	}
	return out
}

func instanceLabel(fqdn, service string) string {
	label := fqdn
	if i := strings.Index(strings.ToLower(fqdn), "."+strings.ToLower(service)); i >= 0 {
		label = fqdn[:i]
	}
	var b strings.Builder
	for i := 0; i < len(label); i++ {
		if label[i] == '\\' && i+3 < len(label) && isDigit(label[i+1]) && isDigit(label[i+2]) && isDigit(label[i+3]) {
			b.WriteByte(byte((label[i+1]-'0')*100 + (label[i+2]-'0')*10 + (label[i+3] - '0')))
			i += 3
			continue
		}
		if label[i] == '\\' && i+1 < len(label) {
			i++
		}
		b.WriteByte(label[i])
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
