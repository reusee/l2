package l2

import (
	"encoding/json"
	"net"
	"runtime"
	"testing"

	"github.com/vishvananda/netns"
)

func TestAll(t *testing.T) {
	defer func() {
		var err error
		he(&err)
		if err != nil {
			t.Fatal(err)
		}
	}()

	node1 := &Node{
		LanIP: net.IPv4(192, 168, 42, 1),
	}
	node2 := &Node{
		LanIP: net.IPv4(192, 168, 42, 2),
	}

	nodes := []*Node{
		node1, node2,
	}
	var network1, network2 *Network

	// node1
	var ln net.Listener
	closed1 := make(chan struct{})
	ok1 := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		ns0, err := netns.Get()
		ce(err)
		_ = ns0
		ns1, err := netns.New()
		ce(err)
		_ = ns1

		network1 = &Network{
			Network: net.IPNet{
				IP:   net.IPv4(192, 168, 42, 0),
				Mask: net.CIDRMask(24, 32),
			},
			Nodes:     nodes,
			MTU:       1345,
			CryptoKey: []byte("1234567890123456"),
			SelectNode: func() *Node {
				return node1
			},
			OnFrame: func(bs []byte) {
				c := make([]byte, len(bs))
				copy(c, bs)
				select {
				case network2.InjectFrame <- c:
				case <-network2.closing:
				}
			},
		}
		err = network1.Start()
		ce(err)
		if !network1.Network.Contains(network1.localNode.LanIP) {
			t.Fatal()
		}

		ln, err = net.Listen("tcp", node1.LanIP.String()+":34567")
		ce(err)
		go func() {
			conn, err := ln.Accept()
			ce(err)
			var obj interface{}
			ce(json.NewDecoder(conn).Decode(&obj))
			ce(json.NewEncoder(conn).Encode(obj))
			conn.Close()
			close(closed1)
		}()

		close(ok1)
	}()

	<-ok1
	network2 = &Network{
		Network: net.IPNet{
			IP:   net.IPv4(192, 168, 42, 0),
			Mask: net.CIDRMask(24, 32),
		},
		Nodes:     nodes,
		MTU:       1345,
		CryptoKey: []byte("1234567890123456"),
		SelectNode: func() *Node {
			return node2
		},
		OnFrame: func(bs []byte) {
			c := make([]byte, len(bs))
			copy(c, bs)
			select {
			case network1.InjectFrame <- c:
			case <-network1.closing:
			}
		},
	}
	err := network2.Start()
	ce(err)
	if !network2.Network.Contains(network2.localNode.LanIP) {
		t.Fatal()
	}

	conn, err := net.Dial("tcp", node1.LanIP.String()+":34567")
	ce(err)
	ce(json.NewEncoder(conn).Encode("foo"))
	var s string
	ce(json.NewDecoder(conn).Decode(&s))
	if s != "foo" {
		t.Fatal()
	}
	conn.Close()

	ln.Close()
	<-closed1

	network1.Close()
	network2.Close()

}
