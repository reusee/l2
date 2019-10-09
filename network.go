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

	LocalNode *Node

	nodes     atomic.Value
	ifaces    []*water.Interface
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

var (
	EthernetBroadcast = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
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
	n.LocalNode = localNode
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

	outboundSenderGroup := new(sync.WaitGroup)
	var ifaceDoChans []chan func()
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

				var destIP *net.IP
				var destAddr *net.HardwareAddr
				parser.DecodeLayers(bs, &decoded)

				for _, t := range decoded {
					switch t {

					case layers.LayerTypeEthernet:
						// skip ipv6
						if eth.EthernetType == layers.EthernetTypeIPv6 {
							continue loop
						}
						// dest mac
						if !bytes.Equal(eth.DstMAC, EthernetBroadcast) {
							addr := make(net.HardwareAddr, len(eth.DstMAC))
							copy(addr, eth.DstMAC)
							destAddr = &addr
						}

					case layers.LayerTypeARP:
						// dest ip
						ip := make(net.IP, len(arp.DstProtAddress))
						copy(ip, arp.DstProtAddress)
						destIP = &ip

					case layers.LayerTypeIPv4:
						// skip
						if !n.Network.Contains(ipv4.DstIP) {
							continue loop
						}
						// dest ip
						ip := make(net.IP, len(ipv4.DstIP))
						copy(ip, ipv4.DstIP)
						destIP = &ip

					}
				}

				if n.OnFrame != nil {
					n.OnFrame(bs)
				}

				sn := atomic.AddUint64(&serial, 1)
				for _, ch := range outboundChans {
					eth := getBytes(l)
					copy(eth.Bytes, bs)
					select {
					case ch <- &Outbound{
						Eth:      eth,
						Serial:   sn,
						DestIP:   destIP,
						DestAddr: destAddr,
					}:
					case <-closing:
						eth.Put()
					}
				}

			}
		})

		// bridge -> interface
		doChan := make(chan func(), 1024)
		ifaceDoChans = append(ifaceDoChans, doChan)
		spawn(scope, func(
			closing Closing,
		) {
			for {
				select {
				case <-closing:
					return
				case fn := <-doChan:
					fn()
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
		var ipv4 layers.IPv4
		parser.AddDecodingLayer(&eth)
		parser.AddDecodingLayer(&arp)
		parser.AddDecodingLayer(&ipv4)
		decoded := make([]gopacket.LayerType, 0, 10)
		dedup := make(map[uint64][]uint64)
		macBytes := make([]byte, 8)

	loop_inbound:
		for {
			select {

			case inbound := <-inboundChan:
				if inbound == nil {
					break
				}

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
							inbound.Eth.Put()
							continue loop_inbound
						}
						m[inbound.Serial%(1<<17)] = inbound.Serial

					}
				}

				num := int32(len(n.ifaces))
				for i, ch := range ifaceDoChans {
					i := i
					ch <- func() {
						n.ifaces[i].Write(inbound.Eth.Bytes)
						if atomic.AddInt32(&num, -1) <= 0 {
							inbound.Eth.Put()
						}
					}
				}

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
