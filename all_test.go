package l2

import (
	"net"
	"runtime"
	"testing"
)

func TestAll(t *testing.T) {

	nodes := []*Node{
		{
			IP: net.IPv4(192, 168, 42, 1),
		},
		{
			IP: net.IPv4(192, 168, 42, 2),
		},
		{
			IP: net.IPv4(192, 168, 42, 3),
		},
	}

	network := &Network{
		Network: net.IPNet{
			IP:   net.IPv4(192, 168, 42, 0),
			Mask: net.CIDRMask(24, 32),
		},
		Nodes:     nodes,
		MTU:       1345,
		CryptoKey: []byte("1234567890123456"),
		SelectNode: func() *Node {
			return nil
		},
	}

	numG := runtime.NumGoroutine()
	if err := network.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		network.Close()
		if numG != runtime.NumGoroutine() {
			pt("%d %d\n", numG, runtime.NumGoroutine())
			t.Fatalf("goroutine leaked")
		}
	}()

	if !network.Network.Contains(network.localNode.IP) {
		t.Fatal()
	}

	//TODO
}
