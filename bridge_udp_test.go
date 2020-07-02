package l2

import (
	"encoding/json"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/vishvananda/netns"
)

func TestPingPongAllInitNodesUDP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 4),
		WanHost:     "localhost",
		BridgeNames: []string{"UDP"},
	}
	node2 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 5),
		WanHost:     "localhost",
		BridgeNames: []string{"UDP"},
		ID:          2,
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
				MTU:       testMTU,
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
				MTU:       testMTU,
				CryptoKey: testCryptoKey,
				SelectNode: func() *Node {
					return node2
				},
			}
		},
	)
}

func TestPingPongOneRandomNodeUDP(t *testing.T) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 6),
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
				MTU:       testMTU,
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
				MTU:       testMTU,
				CryptoKey: testCryptoKey,
			}
		},
	)
}

func BenchmarkUDP(b *testing.B) {
	node1 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 1),
		WanHost:     "localhost",
		BridgeNames: []string{"UDP"},
	}
	node2 := &Node{
		LanIP:       net.IPv4(192, 168, 244, 2),
		WanHost:     "localhost",
		BridgeNames: []string{"UDP"},
		ID:          2,
	}
	nodes := []*Node{
		node1, node2,
	}
	network1 := &Network{
		Network: net.IPNet{
			IP:   net.IPv4(192, 168, 244, 0),
			Mask: net.CIDRMask(24, 32),
		},
		InitNodes: nodes,
		MTU:       testMTU,
		CryptoKey: testCryptoKey,
		SelectNode: func() *Node {
			return node1
		},
	}
	network2 := &Network{
		Network: net.IPNet{
			IP:   net.IPv4(192, 168, 244, 0),
			Mask: net.CIDRMask(24, 32),
		},
		InitNodes: nodes,
		MTU:       testMTU,
		CryptoKey: testCryptoKey,
		SelectNode: func() *Node {
			return node2
		},
	}

	ok := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		_, err := netns.New()
		ce(err)
		ce(network1.Start())

		ln, err := net.Listen("tcp", network1.LocalNode.LanIP.String()+":34567")
		ce(err)
		network1.Scope.Call(func(
			spawn Spawn,
			scope Scope,
		) {
			network1.Scope.Call(func(
				on On,
			) {
				on(EvNetworkClosing, func() {
					ln.Close()
				})
			})
			spawn(scope, func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					spawn(scope, func() {
						defer conn.Close()
						for {
							var s string
							ce(json.NewDecoder(conn).Decode(&s))
							ce(json.NewEncoder(conn).Encode(s))
							if s == "quit" {
								return
							}
						}
					})
				}
			})
		})

		close(ok)
	}()
	<-ok

	ce(network2.Start())

	retry := 10
connect:
	conn, err := net.Dial("tcp", network1.LocalNode.LanIP.String()+":34567")
	if err != nil && retry > 0 {
		time.Sleep(time.Millisecond * 200)
		retry--
		goto connect
	}
	ce(err)

	var s string
	input := "foobar"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ce(json.NewEncoder(conn).Encode(input))
		ce(json.NewDecoder(conn).Decode(&s))
		if s != input {
			b.Fatal()
		}
	}
	b.StopTimer()

	ce(json.NewEncoder(conn).Encode("quit"))
	ce(json.NewDecoder(conn).Decode(&s))
	conn.Close()

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go func() {
		defer wg.Done()
		network1.Close()
	}()
	go func() {
		defer wg.Done()
		network2.Close()
	}()
	wg.Wait()

}
