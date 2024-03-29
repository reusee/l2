package l2

import (
	"encoding/binary"
	"math/rand"
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
		n.BridgeNames = []string{BridgeTCP}
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

func (n *Node) HasBridge(name string) bool {
	for _, bridge := range n.BridgeNames {
		if bridge == name {
			return true
		}
	}
	return false
}

func (Network) LocalNode(
	selectNode SelectNode,
	ipnet net.IPNet,
	initNodes InitNodes,
) *Node {

	if selectNode != nil {
		if n := selectNode(); n != nil {
			return n
		}
	}

	// random ip
	ones, _ := ipnet.Mask.Size()
random_ip:
	ip := ipnet.IP.Mask(ipnet.Mask)
	bs := make(net.IP, len(ip))
	if len(bs) == 8 {
		num := uint64(rand.Int63()) & (^uint64(0) >> ones)
		if num == 0 || (^num) == 0 {
			goto random_ip
		}
		binary.BigEndian.PutUint64(bs, uint64(num))
	} else {
		num := uint32(rand.Int63()) & (^uint32(0) >> ones)
		if num == 0 || (^num) == 0 {
			goto random_ip
		}
		binary.BigEndian.PutUint32(bs, uint32(num))
	}
	for i, b := range ip {
		ip[i] = b | bs[i]
	}
	for _, node := range initNodes {
		if node.LanIP.Equal(ip) {
			goto random_ip
		}
	}

	return &Node{
		LanIP: ip,
	}

}
