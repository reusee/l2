package l2

import (
	"net"
	"testing"
)

func TestPingPongAllInitNodesUDP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 1),
		WanHost:     "localhost",
		BridgeNames: []string{"UDP"},
	}
	node2 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 2),
		WanHost:     "127.0.0.1",
		BridgeNames: []string{"UDP"},
	}
	nodes := []*Node{
		node1, node2,
	}
	testPingPong(t,
		func() *Network {
			return &Network{
				Network: net.IPNet{
					IP:   net.IPv4(192, 168, 244, 0),
					Mask: net.CIDRMask(24, 32),
				},
				InitNodes: nodes,
				MTU:       1345,
				CryptoKey: []byte("1234567890123456"),
				SelectNode: func() *Node {
					return node1
				},
			}
		},
		func() *Network {
			return &Network{
				Network: net.IPNet{
					IP:   net.IPv4(192, 168, 244, 0),
					Mask: net.CIDRMask(24, 32),
				},
				InitNodes: nodes,
				MTU:       1345,
				CryptoKey: []byte("1234567890123456"),
				SelectNode: func() *Node {
					return node2
				},
			}
		},
	)
}

func TestPingPongOneRandomNodeUDP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 1),
		WanHost:     "localhost",
		BridgeNames: []string{"UDP"},
	}
	nodes := []*Node{
		node1,
	}
	testPingPong(t,
		func() *Network {
			return &Network{
				Network: net.IPNet{
					IP:   net.IPv4(192, 168, 244, 0),
					Mask: net.CIDRMask(24, 32),
				},
				InitNodes: nodes,
				MTU:       1345,
				CryptoKey: []byte("1234567890123456"),
				SelectNode: func() *Node {
					return node1
				},
			}
		},
		func() *Network {
			return &Network{
				Network: net.IPNet{
					IP:   net.IPv4(192, 168, 244, 0),
					Mask: net.CIDRMask(24, 32),
				},
				InitNodes: nodes,
				MTU:       1345,
				CryptoKey: []byte("1234567890123456"),
			}
		},
	)
}
