package l2

import (
	"bytes"
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
	addr1 := &net.UDPAddr{
		IP:   node1.LanIP,
		Port: 4242,
	}
	addr2 := &net.UDPAddr{
		IP:   node2.LanIP,
		Port: 4242,
	}

	nodes := []*Node{
		node1, node2,
	}
	numG := runtime.NumGoroutine()

	// node1
	var conn1 *net.UDPConn
	closed1 := make(chan struct{})
	var network1 *Network
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
		}
		err = network1.Start()
		ce(err)
		if !network1.Network.Contains(network1.localNode.LanIP) {
			t.Fatal()
		}

		conn1, err = net.ListenUDP("udp", addr1)
		ce(err)
		go func() {
			buf := make([]byte, 4096)
			for {
				n, addr, err := conn1.ReadFromUDP(buf)
				if err != nil {
					break
				}
				_, err = conn1.WriteToUDP(buf[:n], addr)
				ce(err)
			}
			close(closed1)
		}()

	}()

	network2 := &Network{
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
	}
	err := network2.Start()
	ce(err)
	if !network2.Network.Contains(network2.localNode.LanIP) {
		t.Fatal()
	}

	conn2, err := net.ListenUDP("udp", addr2)
	ce(err)
	msg := []byte("foo")
	conn2.WriteToUDP([]byte("foo"), addr1)
	buf := make([]byte, 4096)
	n, addr, err := conn2.ReadFromUDP(buf)
	ce(err)
	if !addr.IP.Equal(addr1.IP) {
		t.Fatal()
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatal()
	}
	conn2.Close()

	conn1.Close()
	<-closed1

	network1.Close()
	network2.Close()

	if numG != runtime.NumGoroutine() {
		pt("%d %d\n", numG, runtime.NumGoroutine())
		t.Fatalf("goroutine leaked")
	}

}
