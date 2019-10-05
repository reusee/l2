package l2

import (
	"net"
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
		Nodes: nodes,
	}

	if err := network.Setup(); err != nil {
		t.Fatal(err)
	}

	pt("%+v\n", network.localNode)

	//TODO
}
