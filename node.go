package l2

import (
	"net"
)

type Node struct {
	LanIP       net.IP
	WanHost     string
	BridgeNames []string

	wanIP    net.IP
	lanIPStr string
}

func (n *Node) Init() {
	// defaults
	if len(n.BridgeNames) == 0 {
		n.BridgeNames = allBridgeNames
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
