package l2

import (
	"net"
	"testing"
)

func TestPingPongAllInitNodesTCP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 1),
		WanHost:     "localhost",
		BridgeNames: []string{"TCP"},
	}
	node2 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 2),
		WanHost:     "127.0.0.1",
		BridgeNames: []string{"TCP"},
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
				CryptoKey: testCryptoKey,
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
				CryptoKey: testCryptoKey,
				SelectNode: func() *Node {
					return node2
				},
			}
		},
	)
}

func TestPingPongOneRandomNodeTCP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 1),
		WanHost:     "localhost",
		BridgeNames: []string{"TCP"},
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
				CryptoKey: testCryptoKey,
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
				CryptoKey: testCryptoKey,
			}
		},
	)
}
