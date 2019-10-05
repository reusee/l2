package l2

import (
	"encoding/binary"
	"math/rand"
	"net"
	"os"
	"sync"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/reusee/dscope"
	"github.com/songgao/water"
)

type Network struct {
	Network   net.IPNet
	Nodes     []*Node
	MTU       int
	CryptoKey []byte

	SelectNode dyn

	localNode *Node
	ifaces    []*water.Interface
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
			if node.LanIP.Equal(ip) {
				goto random_ip
			}
		}
		localNode = &Node{
			LanIP: ip,
		}
	}

	// add local node to network
	n.localNode = localNode
	var existed bool
	for _, node := range n.Nodes {
		if node.LanIP.Equal(localNode.LanIP) {
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
			node.BridgeNames = allBridgeNames
		}
	}

	// setup interface
	if n.MTU == 0 {
		n.MTU = 1300
	}
	n.SetupInterfaces()

	// outbounds
	outboundPayloads := make(chan Bytes)
	for _, iface := range n.ifaces {
		iface := iface
		go func() {
			buf := make([]byte, n.MTU+14)
			for {
				n, err := iface.Read(buf)
				ce(err, "read from interface")
				payload := getBytes(n)
				copy(payload.Bytes, buf[:n])
				outboundPayloads <- payload
			}
		}()
	}

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
			return spawn,
				closing
		},
	)

	// start bridges
	for _, name := range localNode.BridgeNames {
		bridge, ok := availableBridges[name]
		if !ok {
			ce(me(nil, "no such bridge: %s", name))
		}
		spawn(scope, bridge.Start)
	}

	// handle outbounds
	spawn(scope, func(
		closing Closing,
	) {
		for {
			select {

			case bs := <-outboundPayloads:
				payload := bs.Bytes

				packet := gopacket.NewPacket(
					payload,
					layers.LayerTypeEthernet,
					gopacket.DecodeOptions{
						Lazy:   true,
						NoCopy: true,
					},
				)
				ethernet := packet.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
				if ethernet.EthernetType != layers.EthernetTypeIPv4 &&
					ethernet.EthernetType != layers.EthernetTypeARP {
					break
				}

				pt("----------\n")
				for _, layer := range packet.Layers() {
					pt("%v\n", layer.LayerType())
					pt("%T\n", layer)
					pt("%+v\n", layer)
				}
				pt("\n\n\n\n")

				bs.Put()

			case <-closing:
				return

			}
		}
	})

	return nil
}

func (n *Network) Close() {
	close(n.closing)
	n.closed.Wait()
}
