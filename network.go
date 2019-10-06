package l2

import (
	"bytes"
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

	SelectNode  dyn
	OnFrame     func([]byte)
	InjectFrame chan ([]byte)

	localNode *Node
	ifaces    []*water.Interface
	closing   chan struct{}
	waitClose sync.WaitGroup
	closeOnce sync.Once
}

type (
	InterfaceAddrs []net.Addr
	Hostname       string
	Spawn          func(Scope, any)
	Closing        chan struct{}
	Ready          chan struct{}
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

	// nodes
	for _, node := range n.Nodes {
		node.Init()
	}

	// setup interface
	if n.MTU == 0 {
		n.MTU = 1300
	}
	n.SetupInterfaces()

	// scope
	closing := make(chan struct{})
	n.closing = closing
	spawn := func(scope Scope, fn any) {
		n.waitClose.Add(1)
		go func() {
			defer n.waitClose.Done()
			scope.Call(fn)
		}()
	}
	scope := dscope.New(
		func() (
			Spawn,
			Closing,
			*Network,
		) {
			return spawn,
				closing,
				n
		},
	)

	var outboundChans []chan Outbound
	inboundChan := make(chan Inbound, 1024)

	// start bridges
	for _, name := range localNode.BridgeNames {
		bridge, ok := availableBridges[name]
		if !ok {
			ce(me(nil, "no such bridge: %s", name))
		}
		outboundCh := make(chan Outbound, 1024)
		outboundChans = append(outboundChans, outboundCh)
		ready := make(chan struct{})
		spawn(scope.Sub(
			func() chan Outbound {
				return outboundCh
			},
			func() chan Inbound {
				return inboundChan
			},
			func() Ready {
				return ready
			},
		), bridge.Start)
		<-ready
	}

	for _, iface := range n.ifaces {
		iface := iface

		// interface -> bridge
		spawn(scope, func() {

			buf := make([]byte, n.MTU+14)
			parser := gopacket.NewDecodingLayerParser(layers.LayerTypeEthernet)
			parser.SetDecodingLayerContainer(gopacket.DecodingLayerSparse(nil))
			var eth layers.Ethernet
			var arp layers.ARP
			var ipv4 layers.IPv4
			parser.AddDecodingLayer(&eth)
			parser.AddDecodingLayer(&ipv4)
			parser.AddDecodingLayer(&arp)
			decoded := make([]gopacket.LayerType, 0, 10)

		loop:
			for {
				l, err := iface.Read(buf)
				if err != nil {
					select {
					case <-closing:
						return
					default:
						ce(err, "read from interface")
					}
				}
				bs := buf[:l]

				isBroadcast := false
				var destNode *Node

				parser.DecodeLayers(bs, &decoded)

				for _, t := range decoded {
					switch t {

					case layers.LayerTypeEthernet:
						for _, node := range n.Nodes {
							node.RLock()
							for _, addr := range node.macAddrs {
								if bytes.Equal(addr, eth.DstMAC) {
									destNode = node
								}
							}
							node.RUnlock()
						}
						if eth.EthernetType == layers.EthernetTypeIPv6 {
							continue loop
						}

					case layers.LayerTypeARP:
						ip := net.IP(arp.SourceProtAddress)
						for _, node := range n.Nodes {
							if node.LanIP.Equal(ip) {
								node.Lock()
								existed := false
								hwAddr := net.HardwareAddr(arp.SourceHwAddress)
								for _, addr := range node.macAddrs {
									if bytes.Equal(addr, hwAddr) {
										existed = true
										break
									}
								}
								if !existed {
									node.macAddrs = append(node.macAddrs, hwAddr)
								}
								node.Unlock()
							}
						}

					case layers.LayerTypeIPv4:
						if !n.Network.Contains(ipv4.DstIP) {
							pt("drop non-network\n")
							continue loop
						}

					}
				}

				if n.OnFrame != nil {
					n.OnFrame(bs)
				}

				if destNode == nil {
					isBroadcast = true
				}

				pt("%s %d read from interface\n", n.localNode.lanIPStr, hash64(bs))

				if destNode != nil {
					pt("%v\n", destNode)
					dumpEth(bs)
				}

				for _, ch := range outboundChans {
					eth := getBytes(l)
					copy(eth.Bytes, bs)
					select {
					case ch <- Outbound{
						Eth:         eth,
						IsBroadcast: isBroadcast,
						DestNode:    destNode,
					}:
					case <-closing:
						eth.Put()
					}
				}

			}
		})

		// interface <- bridge
		spawn(scope, func(
			closing Closing,
		) {

			parser := gopacket.NewDecodingLayerParser(layers.LayerTypeEthernet)
			parser.SetDecodingLayerContainer(gopacket.DecodingLayerSparse(nil))
			var eth layers.Ethernet
			var arp layers.ARP
			parser.AddDecodingLayer(&eth)
			parser.AddDecodingLayer(&arp)
			decoded := make([]gopacket.LayerType, 0, 10)

			for {
				select {

				case inbound := <-inboundChan:
					pt("%s %d write to interface\n", n.localNode.lanIPStr, hash64(inbound.Eth.Bytes))

					parser.DecodeLayers(inbound.Eth.Bytes, &decoded)

					for _, t := range decoded {
						switch t {

						case layers.LayerTypeARP:
							ip := net.IP(arp.SourceProtAddress)
							for _, node := range n.Nodes {
								if node.LanIP.Equal(ip) {
									node.Lock()
									existed := false
									hwAddr := net.HardwareAddr(arp.SourceHwAddress)
									for _, addr := range node.macAddrs {
										if bytes.Equal(addr, hwAddr) {
											existed = true
											break
										}
									}
									if !existed {
										node.macAddrs = append(node.macAddrs, hwAddr)
									}
									node.Unlock()
								}
							}

						}
					}

					iface.Write(inbound.Eth.Bytes)

					inbound.Eth.Put()

				case <-closing:
					return

				}
			}
		})

	}

	// interface <- bridge
	n.InjectFrame = make(chan []byte, 1024)

	spawn(scope, func(
		closing Closing,
	) {
		for {
			select {

			case bs := <-n.InjectFrame:
				n.ifaces[0].Write(bs)

			case <-closing:
				return

			}
		}
	})

	return nil
}

func (n *Network) Close() {
	n.closeOnce.Do(func() {
		close(n.closing)
		for _, iface := range n.ifaces {
			iface.Close()
		}
		n.waitClose.Wait()
	})
}
