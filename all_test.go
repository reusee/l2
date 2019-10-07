package l2

import (
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishvananda/netns"
)

func TestPingPongAllInitNodes(t *testing.T) {
	node1 := &Node{
		LanIP:   net.IPv4(192, 168, 42, 1),
		WanHost: "localhost",
	}
	node2 := &Node{
		LanIP:   net.IPv4(192, 168, 42, 2),
		WanHost: "127.0.0.1",
	}
	nodes := []*Node{
		node1, node2,
	}
	testPingPong(t,
		func() *Network {
			return &Network{
				Network: net.IPNet{
					IP:   net.IPv4(192, 168, 42, 0),
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
					IP:   net.IPv4(192, 168, 42, 0),
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

func TestPingPongOneRandomNode(t *testing.T) {
	node1 := &Node{
		LanIP:   net.IPv4(192, 168, 42, 1),
		WanHost: "localhost",
	}
	nodes := []*Node{
		node1,
	}
	testPingPong(t,
		func() *Network {
			return &Network{
				Network: net.IPNet{
					IP:   net.IPv4(192, 168, 42, 0),
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
					IP:   net.IPv4(192, 168, 42, 0),
					Mask: net.CIDRMask(24, 32),
				},
				InitNodes: nodes,
				MTU:       1345,
				CryptoKey: []byte("1234567890123456"),
			}
		},
	)
}

func testPingPong(
	t *testing.T,
	getNetwork1 func() *Network,
	getNetwork2 func() *Network,
) {
	defer func() {
		var err error
		he(&err)
		if err != nil {
			t.Fatal(err)
		}
	}()

	// node1
	var ln net.Listener
	ok1 := make(chan struct{})
	var network1 *Network
	go func() {
		runtime.LockOSThread()
		ns0, err := netns.Get()
		ce(err)
		_ = ns0
		ns1, err := netns.New()
		ce(err)
		_ = ns1

		network1 = getNetwork1()
		err = network1.Start()
		ce(err)
		if !network1.Network.Contains(network1.localNode.LanIP) {
			t.Fatal()
		}

		ln, err = net.Listen("tcp", network1.localNode.LanIP.String()+":34567")
		ce(err)
		network1.Scope.Call(func(
			spawn Spawn,
			scope Scope,
		) {
			network1.onClose = append(network1.onClose, func() {
				ln.Close()
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

		close(ok1)
	}()
	<-ok1

	network2 := getNetwork2()
	err := network2.Start()
	ce(err)
	if !network2.Network.Contains(network2.localNode.LanIP) {
		t.Fatal()
	}

	retry := 10
connect:
	conn, err := net.Dial("tcp", network1.localNode.LanIP.String()+":34567")
	if err != nil && retry > 0 {
		time.Sleep(time.Millisecond * 200)
		retry--
		goto connect
	}
	ce(err)
	var s string
	for i := 0; i < 1024; i++ {
		input := strings.Repeat("foobar", i)
		ce(json.NewEncoder(conn).Encode(input))
		ce(json.NewDecoder(conn).Decode(&s))
		if s != input {
			t.Fatal()
		}
	}
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
