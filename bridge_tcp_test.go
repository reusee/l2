package l2

import (
	"net"
	"testing"

	"github.com/reusee/dscope"
)

func TestPingPongAllInitNodesTCP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 1),
		WanHost:     "localhost",
		BridgeNames: []string{"TCP"},
	}
	node2 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 2),
		WanHost:     "localhost",
		BridgeNames: []string{"TCP"},
		ID:          2,
	}
	nodes := []*Node{
		node1, node2,
	}
	testPingPong(t,

		func() *Network {
			return NewNetwork(
				dscope.New(),
				[]any{
					func() net.IPNet {
						return net.IPNet{
							IP:   net.IPv4(192, 168, 244, 0),
							Mask: net.CIDRMask(24, 32),
						}
					},
					func() InitNodes {
						return nodes
					},
					func() MTU {
						return testMTU
					},
					func() CryptoKey {
						return testCryptoKey
					},
					func() SelectNode {
						return func() *Node {
							return node1
						}
					},
				},
			)
		},

		func() *Network {
			return NewNetwork(
				dscope.New(),
				[]any{
					func() net.IPNet {
						return net.IPNet{
							IP:   net.IPv4(192, 168, 244, 0),
							Mask: net.CIDRMask(24, 32),
						}
					},
					func() InitNodes {
						return nodes
					},
					func() MTU {
						return testMTU
					},
					func() CryptoKey {
						return testCryptoKey
					},
					func() SelectNode {
						return func() *Node {
							return node2
						}
					},
				},
			)
		},
	)
}

func TestPingPongOneRandomNodeTCP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 3),
		WanHost:     "localhost",
		BridgeNames: []string{"TCP"},
	}
	nodes := []*Node{
		node1,
	}
	testPingPong(t,

		func() *Network {
			return NewNetwork(
				dscope.New(),
				[]any{
					func() net.IPNet {
						return net.IPNet{
							IP:   net.IPv4(192, 168, 244, 0),
							Mask: net.CIDRMask(24, 32),
						}
					},
					func() InitNodes {
						return nodes
					},
					func() MTU {
						return testMTU
					},
					func() CryptoKey {
						return testCryptoKey
					},
					func() SelectNode {
						return func() *Node {
							return node1
						}
					},
				},
			)
		},

		func() *Network {
			return NewNetwork(
				dscope.New(),
				[]any{
					func() net.IPNet {
						return net.IPNet{
							IP:   net.IPv4(192, 168, 244, 0),
							Mask: net.CIDRMask(24, 32),
						}
					},
					func() InitNodes {
						return nodes
					},
					func() MTU {
						return testMTU
					},
					func() CryptoKey {
						return testCryptoKey
					},
				},
			)
		},
	)
}
