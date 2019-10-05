package l2

import (
	"encoding/binary"
	"math/rand"
	"net"
	"os"
	"sync"

	"github.com/reusee/dscope"
	"github.com/songgao/water"
)

type Network struct {
	Network net.IPNet
	Nodes   []*Node
	Bridges []Bridge
	MTU     int

	SelectNode dyn

	localNode *Node
	iface     *water.Interface
	closing   chan struct{}
	closed    sync.WaitGroup
}

type (
	InterfaceAddrs []net.Addr
	Hostname       string
	Spawn          func(Scope, any)
	Closing        chan struct{}
)

func (n *Network) Start() (err error) {
	defer he(&err)

	// get local node
	var localNode *Node
	if n.SelectNode != nil {
		addrs, err := net.InterfaceAddrs()
		ce(err, "get interface addrs")
		hostname, err := os.Hostname()
		ce(err, "get host name")
		dscope.New(
			func() (
				InterfaceAddrs,
				Hostname,
			) {
				return InterfaceAddrs(addrs),
					Hostname(hostname)
			},
		).Call(n.SelectNode, &localNode)
	}

	// random ip node
	if localNode == nil {
		ones, _ := n.Network.Mask.Size()
	random_ip:
		ip := n.Network.IP.Mask(n.Network.Mask)
		bs := make(net.IP, len(ip))
		if len(bs) == 8 {
			num := uint64(rand.Int63()) & (^uint64(0) >> ones)
			if num == 0 || (^num) == 0 {
				goto random_ip
			}
			binary.BigEndian.PutUint64(bs, uint64(num))
		} else {
			num := uint32(rand.Int63()) & (^uint32(0) >> ones)
			if num == 0 || (^num) == 0 {
				goto random_ip
			}
			binary.BigEndian.PutUint32(bs, uint32(num))
		}
		for i, b := range ip {
			ip[i] = b | bs[i]
		}
		for _, node := range n.Nodes {
			if node.IP.Equal(ip) {
				goto random_ip
			}
		}
		localNode = &Node{
			IP: ip,
		}
	}

	// add local node to network
	n.localNode = localNode
	var existed bool
	for _, node := range n.Nodes {
		if node.IP.Equal(localNode.IP) {
			existed = true
			break
		}
	}
	if !existed {
		n.Nodes = append(n.Nodes, localNode)
	}

	// default bridges
	for _, node := range n.Nodes {
		if len(node.BridgeNames) == 0 {
			node.BridgeNames = []string{"TCP", "UDP"}
		}
	}

	// setup interface
	if n.MTU == 0 {
		n.MTU = 1300
	}
	n.SetupInterface()

	// scope
	closing := make(chan struct{})
	n.closing = closing
	spawn := func(scope Scope, fn any) {
		n.closed.Add(1)
		go func() {
			defer n.closed.Done()
			scope.Call(fn)
		}()
	}
	scope := dscope.New(
		func() (
			Spawn,
			Closing,
		) {
			return spawn, closing
		},
	)

	spawn(scope, func() {
	})

	return nil
}

func (n *Network) Close() {
	close(n.closing)
	n.closed.Wait()
}
