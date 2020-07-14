package l2

import (
	"net"
)

type Node struct {
	LanIP       net.IP
	WanHost     string // reachable from anywhere
	PrivateIP   net.IP // reachable from local lan
	BridgeNames []string
	ID          int // to allow multiple nodes in single host

	wanIP    net.IP
	lanIPStr string
}

func (n *Node) Init() {
	// defaults
	if len(n.BridgeNames) == 0 {
		n.BridgeNames = defaultBridgeNames
	}
	// lookup host
	if n.WanHost != "" {
		addrs, err := net.LookupHost(n.WanHost)
		ce(err)
		for _, addr := range addrs {
			ip := net.ParseIP(addr)
			if len(ip) > 0 {
				n.wanIP = ip
				break
			}
		}
	}
	// ip string
	n.lanIPStr = n.LanIP.String()
}
