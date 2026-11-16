package discovery

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/brutella/dnssd"
	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var lookupType mdnsLookup = multicastLookup

var mdnsGroup6 = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353}

const mdnsMaxPacket = 9000

type mdnsSocket interface {
	readPacket(buf []byte) (n, ifIndex int, err error)
	writePacket(b []byte, ifIndex int) error
	Close() error
}

type socket4 struct {
	pc  *ipv4.PacketConn
	dst *net.UDPAddr
}

func (s socket4) readPacket(buf []byte) (int, int, error) {
	n, cm, _, err := s.pc.ReadFrom(buf)
	if cm == nil {
		return n, 0, err
	}
	return n, cm.IfIndex, err
}

func (s socket4) writePacket(b []byte, ifIndex int) error {
	var cm *ipv4.ControlMessage
	if ifIndex > 0 {
		cm = &ipv4.ControlMessage{IfIndex: ifIndex}
	}
	_ = s.pc.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := s.pc.WriteTo(b, cm, s.dst)
	return err
}

func (s socket4) Close() error { return s.pc.Close() }

type socket6 struct {
	pc  *ipv6.PacketConn
	dst *net.UDPAddr
}

func (s socket6) readPacket(buf []byte) (int, int, error) {
	n, cm, _, err := s.pc.ReadFrom(buf)
	if cm == nil {
		return n, 0, err
	}
	return n, cm.IfIndex, err
}

func (s socket6) writePacket(b []byte, ifIndex int) error {
	var cm *ipv6.ControlMessage
	if ifIndex > 0 {
		cm = &ipv6.ControlMessage{IfIndex: ifIndex}
	}
	_ = s.pc.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := s.pc.WriteTo(b, cm, s.dst)
	return err
}

func (s socket6) Close() error { return s.pc.Close() }

func openMDNSSockets(ifaces []*net.Interface) ([]mdnsSocket, error) {
	var (
		socks []mdnsSocket
		errs  []error
	)
	if conn, err := net.ListenUDP("udp4", mdnsGroup); err != nil {
		errs = append(errs, err)
	} else {
		pc := ipv4.NewPacketConn(conn)
		_ = pc.SetControlMessage(ipv4.FlagInterface, true)
		_ = pc.SetMulticastLoopback(true)
		_ = pc.SetTTL(255)
		_ = pc.SetMulticastTTL(255)
		for _, ifi := range ifaces {
			_ = pc.JoinGroup(ifi, &net.UDPAddr{IP: mdnsGroup.IP})
		}
		socks = append(socks, socket4{pc: pc, dst: mdnsGroup})
	}
	if conn, err := net.ListenUDP("udp6", mdnsGroup6); err != nil {
		errs = append(errs, err)
	} else {
		pc := ipv6.NewPacketConn(conn)
		_ = pc.SetControlMessage(ipv6.FlagInterface, true)
		_ = pc.SetMulticastLoopback(true)
		_ = pc.SetHopLimit(255)
		_ = pc.SetMulticastHopLimit(255)
		for _, ifi := range ifaces {
			_ = pc.JoinGroup(ifi, &net.UDPAddr{IP: mdnsGroup6.IP})
		}
		socks = append(socks, socket6{pc: pc, dst: mdnsGroup6})
	}
	if len(socks) == 0 {
		return nil, errors.Join(errs...)
	}
	return socks, nil
}

func multicastLookup(ctx context.Context, service string, add dnssd.AddFunc, rmv dnssd.RmvFunc) error {
	ifaces := dnssd.MulticastInterfaces()
	socks, err := openMDNSSockets(ifaces)
	if err != nil {
		return err
	}
	indexes := make([]int, len(ifaces))
	for i, ifi := range ifaces {
		indexes[i] = ifi.Index
	}
	return browseSockets(ctx, service, socks, indexes, add, rmv)
}

type mdnsPacket struct {
	msg     *dns.Msg
	ifIndex int
}

func browseSockets(ctx context.Context, service string, socks []mdnsSocket, ifIndexes []int, add dnssd.AddFunc, rmv dnssd.RmvFunc) error {
	rctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		stop()
		for _, s := range socks {
			_ = s.Close()
		}
		wg.Wait()
	}()
	packets := make(chan mdnsPacket)
	for _, s := range socks {
		wg.Go(func() { readPackets(rctx, s, packets) })
	}

	q := new(dns.Msg)
	q.SetQuestion(service, dns.TypePTR)
	q.Id, q.RecursionDesired = 0, false
	if b, err := q.Pack(); err == nil {
		for _, s := range socks {
			if len(ifIndexes) == 0 {
				_ = s.writePacket(b, 0)
			}
			for _, idx := range ifIndexes {
				_ = s.writePacket(b, idx)
			}
		}
	}

	records := newMDNSRecords(service)
	emitted := map[string]dnssd.BrowseEntry{}
	names := map[int]string{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p := <-packets:
			name, ok := names[p.ifIndex]
			if !ok {
				if ifi, err := net.InterfaceByIndex(p.ifIndex); err == nil {
					name = ifi.Name
				}
				names[p.ifIndex] = name
			}
			records.add(p.msg, name)
			current := map[string]dnssd.BrowseEntry{}
			for _, e := range records.entries() {
				key := entryKey(e)
				current[key] = e
				if _, done := emitted[key]; !done {
					emitted[key] = e
					add(e)
				}
			}
			for key, e := range emitted {
				if _, ok := current[key]; !ok {
					delete(emitted, key)
					rmv(e)
				}
			}
		}
	}
}

func entryKey(e dnssd.BrowseEntry) string {
	return e.Name + "\x00" + e.Host + "\x00" + e.IfaceName + "\x00" + strconv.Itoa(e.Port)
}

func readPackets(ctx context.Context, s mdnsSocket, out chan<- mdnsPacket) {
	buf := make([]byte, mdnsMaxPacket)
	for {
		n, ifIndex, err := s.readPacket(buf)
		if err != nil {
			return
		}
		m := new(dns.Msg)
		if m.Unpack(buf[:n]) != nil || !m.Response || m.Opcode != dns.OpcodeQuery || m.Rcode != dns.RcodeSuccess {
			continue
		}
		select {
		case out <- mdnsPacket{msg: m, ifIndex: ifIndex}:
		case <-ctx.Done():
			return
		}
	}
}
