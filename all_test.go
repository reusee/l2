package l2

import (
	"net"
	"runtime"
	"testing"
	"time"
)

func TestAll(t *testing.T) {

	node1 := &Node{
		LanIP: net.IPv4(192, 168, 42, 1),
	}
	node2 := &Node{
		LanIP: net.IPv4(192, 168, 42, 2),
	}
	node3 := &Node{
		LanIP: net.IPv4(192, 168, 42, 3),
	}

	nodes := []*Node{
		node1, node2, node3,
	}
	numG := runtime.NumGoroutine()

	var networks []*Network
	for _, node := range nodes {
		node := node
		network := &Network{
			Network: net.IPNet{
				IP:   net.IPv4(192, 168, 42, 0),
				Mask: net.CIDRMask(24, 32),
			},
			Nodes:     nodes,
			MTU:       1345,
			CryptoKey: []byte("1234567890123456"),
			SelectNode: func() *Node {
				return node
			},
		}
		if err := network.Start(); err != nil {
			t.Fatal(err)
		}
		if !network.Network.Contains(network.localNode.LanIP) {
			t.Fatal()
		}
		networks = append(networks, network)
	}

	//TODO
	time.Sleep(time.Hour)

	for _, network := range networks {
		network.Close()
	}
	if numG != runtime.NumGoroutine() {
		pt("%d %d\n", numG, runtime.NumGoroutine())
		t.Fatalf("goroutine leaked")
	}

}
