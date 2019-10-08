package l2

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/reusee/dscope"
	"github.com/songgao/water"
)

type Network struct {
	Network   net.IPNet
	InitNodes []*Node
	MTU       int
	CryptoKey []byte

	Scope       Scope
	SelectNode  dyn
	OnFrame     func([]byte)
	InjectFrame chan ([]byte)

	nodes     atomic.Value
	localNode *Node
	ifaces    []*water.Interface
	ifaceMACs []net.HardwareAddr
	closing   chan struct{}
	waitClose sync.WaitGroup
	closeOnce sync.Once
	onClose   []func()
}

type (
	Hostname string
	Spawn    func(Scope, any)
	Closing  chan struct{}
	Ready    chan struct{}
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
				[]net.Addr,
				Hostname,
			) {
				return addrs,
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
		for _, node := range n.InitNodes {
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
	for _, node := range n.InitNodes {
		if node.LanIP.Equal(localNode.LanIP) {
			existed = true
			break
		}
	}
	if !existed {
		n.InitNodes = append(n.InitNodes, localNode)
	}

	// nodes
	for _, node := range n.InitNodes {
		node.Init()
	}
	n.nodes.Store(n.InitNodes)

	// setup interface
	if n.MTU == 0 {
		n.MTU = 1300
	}
	n.SetupInterfaces()
	ifaces, err := net.Interfaces()
	ce(err)
	n.ifaceMACs = make([]net.HardwareAddr, len(n.ifaces))
	for i, f1 := range n.ifaces {
		for _, f2 := range ifaces {
			if f2.Name == f1.Name() {
				n.ifaceMACs[i] = f2.HardwareAddr
				break
			}
		}
	}

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
	n.Scope = scope

	var outboundChans []chan *Outbound
	inboundChan := make(chan *Inbound, 1024)

	// start bridges
	inboundSenderGroup := new(sync.WaitGroup)
	for _, name := range localNode.BridgeNames {
		bridge, ok := availableBridges[name]
		if !ok {
			ce(me(nil, "no such bridge: %s", name))
		}
		outboundCh := make(chan *Outbound, 1024)
		outboundChans = append(outboundChans, outboundCh)
		ready := make(chan struct{})
		spawn(scope.Sub(
			func() chan *Outbound {
				return outboundCh
			},
			func() chan *Inbound {
				return inboundChan
			},
			func() Ready {
				return ready
			},
			func() *sync.WaitGroup {
				return inboundSenderGroup
			},
		), bridge.Start)
		<-ready
	}

	n.onClose = append(n.onClose, func() {
		inboundSenderGroup.Wait()
		close(inboundChan)
		for inbound := range inboundChan {
			inbound.Eth.Put()
		}
	})

	// mac address
	updateNodeMAC2 := func(ipBytes []byte, mac []byte) {
		ip := net.IP(ipBytes)
		for _, node := range n.nodes.Load().([]*Node) {
			if node.LanIP.Equal(ip) {
				node.Lock()
				existed := false
				hwAddr := net.HardwareAddr(mac)
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
	updateNodeMAC := func(arp layers.ARP) {
		updateNodeMAC2(arp.SourceProtAddress, arp.SourceHwAddress)
		if arp.Operation == 2 {
			updateNodeMAC2(arp.DstProtAddress, arp.DstHwAddress)
		}
	}

	outboundSenderGroup := new(sync.WaitGroup)
	for _, iface := range n.ifaces {
		iface := iface
		outboundSenderGroup.Add(1)

		// interface -> bridge
		spawn(scope, func() {
			defer outboundSenderGroup.Done()

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
			serial := rand.Uint64()

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
						for _, node := range n.nodes.Load().([]*Node) {
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
						updateNodeMAC(arp)

					case layers.LayerTypeIPv4:
						if !n.Network.Contains(ipv4.DstIP) {
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

				sn := atomic.AddUint64(&serial, 1)
				for _, ch := range outboundChans {
					eth := getBytes(l)
					copy(eth.Bytes, bs)
					select {
					case ch <- &Outbound{
						Eth:         eth,
						IsBroadcast: isBroadcast,
						DestNode:    destNode,
						Serial:      sn,
					}:
					case <-closing:
						eth.Put()
					}
				}

			}
		})

	}
	n.onClose = append(n.onClose, func() {
		for _, iface := range n.ifaces {
			iface.Close()
		}
		outboundSenderGroup.Wait()
		for _, ch := range outboundChans {
			close(ch)
			for outbound := range ch {
				outbound.Eth.Put()
			}
		}
	})

	// interface <- bridge
	n.InjectFrame = make(chan []byte, 1024)

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
		broadcastMAC := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
		dedup := make(map[uint64][]uint64)
		macBytes := make([]byte, 8)

		for {
			select {

			case inbound := <-inboundChan:
				if inbound == nil {
					break
				}
				func() {
					defer inbound.Eth.Put()

					parser.DecodeLayers(inbound.Eth.Bytes, &decoded)

					for _, t := range decoded {
						switch t {

						case layers.LayerTypeEthernet:
							copy(macBytes, eth.SrcMAC)
							macInt := binary.LittleEndian.Uint64(macBytes)
							m, ok := dedup[macInt]
							if !ok {
								m = make([]uint64, 1<<17)
								dedup[macInt] = m
							}
							if m[inbound.Serial%(1<<17)] == inbound.Serial {
								return
							}
							m[inbound.Serial%(1<<17)] = inbound.Serial

						case layers.LayerTypeARP:
							// check for new node
							lanIP := make(net.IP, len(arp.SourceProtAddress))
							copy(lanIP, arp.SourceProtAddress)
							var newNodes []*Node
							nodes := n.nodes.Load().([]*Node)
							existed := false
							for _, node := range nodes {
								if node.LanIP.Equal(lanIP) {
									existed = true
									break
								}
							}
							if !existed {
								macAddr := make(net.HardwareAddr, len(arp.SourceHwAddress))
								copy(macAddr, arp.SourceHwAddress)
								node := &Node{
									LanIP:    lanIP,
									macAddrs: []net.HardwareAddr{macAddr},
								}
								node.Init()
								newNodes = append(newNodes, node)
								if inbound.OnNodeFound != nil {
									inbound.OnNodeFound(node)
								}
							}
							if len(newNodes) > 0 {
								n.nodes.Store(append(nodes, newNodes...))
							}

							updateNodeMAC(arp)

						}
					}

					if bytes.Equal(eth.DstMAC, broadcastMAC) {
						for _, iface := range n.ifaces {
							_, err := iface.Write(inbound.Eth.Bytes)
							ce(err)
						}
					} else {
						for i, mac := range n.ifaceMACs {
							if bytes.Equal(mac, eth.DstMAC) {
								_, err := n.ifaces[i].Write(inbound.Eth.Bytes)
								ce(err)
							}
						}
					}

				}()

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
		for _, fn := range n.onClose {
			fn()
		}
		n.waitClose.Wait()
	})
}
