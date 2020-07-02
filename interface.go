package l2

import (
	"crypto/sha256"
	"net"
	"sync"
)

var ipMac sync.Map

func ip2Mac(ip net.IP) net.HardwareAddr {
	if ip.IsMulticast() {
		return EthernetBroadcast
	}
	if v, ok := ipMac.Load(ip.String()); ok {
		return v.(net.HardwareAddr)
	}
	h := sha256.New()
	h.Write(ip)
	sum := h.Sum(nil)
	mac := make(net.HardwareAddr, 6)
	copy(mac[1:], sum[:5])
	mac[0] = 0x88
	ipMac.Store(ip.String(), mac)
	return mac
}
