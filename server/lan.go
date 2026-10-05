package server

import (
	"net"
	"slices"
)

// Addresses another device can reach this machine at, for the web
// client's link (atto serve beyond loopback, /remote).

// HostAddr is one address of a network interface.
type HostAddr struct {
	Iface    string
	IP       net.IP
	Up       bool
	Loopback bool
}

// LANHosts lists the addresses of this machine's interfaces that another
// device may reach, best first (see PickHosts).
func LANHosts() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var addrs []HostAddr
	for _, ifc := range ifaces {
		as, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range as {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addrs = append(addrs, HostAddr{Iface: ifc.Name, IP: ipn.IP, Up: ifc.Flags&net.FlagUp != 0, Loopback: ifc.Flags&net.FlagLoopback != 0})
		}
	}
	return PickHosts(addrs)
}

// PickHosts orders the addresses of up, non-loopback interfaces: private
// IPv4 (192.168/16, 10/8, 172.16/12) first, as a phone on the same Wi-Fi
// reaches those, then other IPv4 (such as a Tailscale 100.x address), then
// global IPv6. Link-local addresses are left out.
func PickHosts(addrs []HostAddr) []string {
	rank := func(ip net.IP) int {
		switch v4 := ip.To4(); {
		case v4 != nil && v4[0] == 192 && v4[1] == 168:
			return 0
		case v4 != nil && v4[0] == 10:
			return 1
		case v4 != nil && v4[0] == 172 && v4[1]&0xf0 == 16:
			return 2
		case v4 != nil:
			return 3
		default:
			return 4
		}
	}
	type cand struct {
		host string
		rank int
	}
	var out []cand
	seen := map[string]bool{}
	for _, a := range addrs {
		ip := a.IP
		if !a.Up || a.Loopback || ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		host := ip.String()
		if ip.To4() == nil {
			host = "[" + host + "]"
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, cand{host, rank(ip)})
	}
	slices.SortStableFunc(out, func(a, b cand) int { return a.rank - b.rank })
	hosts := make([]string, len(out))
	for i, c := range out {
		hosts[i] = c.host
	}
	return hosts
}

// URLHosts are the hosts to put in links for a server listening on addr:
// its own host, or for a wildcard address (0.0.0.0, ::, "") this
// machine's LAN addresses.
func URLHosts(addr string) []string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		return LANHosts()
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return []string{"[" + host + "]"}
	}
	return []string{host}
}
