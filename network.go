package l2

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beevik/ntp"
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

func (n *Network) Start(fns ...dyn) (err error) {
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

	// utils
	var getTime = func() func() time.Time {
		servers := []string{
			"time.cloudflare.com",
			"cn.ntp.org.cn",
		}
		ret := make(chan time.Time, 1)
		for _, server := range servers {
			server := server
			go func() {
				t0, err := ntp.Time(server)
				if err != nil {
					return
				}
				select {
				case ret <- t0:
				default:
				}
			}()
		}
		select {
		case ntpTime0 := <-ret:
			ntpTime0 = ntpTime0.UTC()
			sysTime0 := time.Now()
			return func() time.Time {
				return ntpTime0.Add(time.Since(sysTime0))
			}
		case <-time.After(time.Second * 3):
			panic("get ntp time timeout")
		}
	}()

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
			func() time.Time,
		) {
			return spawn,
				closing,
				n,
				getTime
		},
		Ev,
	)
	n.Scope = scope
	var on On
	var trigger Trigger
	scope.Assign(&on, &trigger)

	for _, fn := range fns {
		scope.Call(fn)
	}

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

	on(EvNetworkClosing, func() {
		inboundSenderGroup.Wait()
	})

	outboundSenderGroup := new(sync.WaitGroup)
	var ifaceDoChans []chan func()
	var ifaceHardwareAddrs []net.HardwareAddr
	for _, iface := range n.ifaces {
		iface := iface
		outboundSenderGroup.Add(1)

		netInterface, err := net.InterfaceByName(iface.Name())
		ce(err)
		ifaceHardwareAddrs = append(ifaceHardwareAddrs, netInterface.HardwareAddr)

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
				eth := make([]byte, l)
				copy(eth, bs)
				outbound := &Outbound{
					Eth:      eth,
					Serial:   sn,
					DestIP:   destIP,
					DestAddr: destAddr,
				}
				for _, ch := range outboundChans {
					select {
					case ch <- outbound:
					case <-closing:
					}
				}
				trigger(scope.Sub(
					func() *Outbound {
						return outbound
					},
				), EvNetwork, EvNetworkOutboundSent)

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
	on(EvNetworkClosing, func() {
		for _, iface := range n.ifaces {
			iface.Close()
		}
		outboundSenderGroup.Wait()
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

				trigger(scope.Sub(
					func() *Inbound {
						return inbound
					},
				), EvNetwork, EvNetworkInboundReceived)

				parser.DecodeLayers(inbound.Eth, &decoded)

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
							trigger(scope.Sub(
								func() *Inbound {
									return inbound
								},
							), EvNetwork, EvNetworkInboundDuplicated)
							continue loop_inbound
						}
						m[inbound.Serial%(1<<17)] = inbound.Serial

					}
				}

				for i, ch := range ifaceDoChans {
					if inbound.DestAddr != nil && !bytes.Equal(*inbound.DestAddr, ifaceHardwareAddrs[i]) {
						continue
					}
					iface := n.ifaces[i]
					ch <- func() {
						iface.Write(inbound.Eth)
					}
					break
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
		n.Scope.Call(func(
			trigger Trigger,
		) {
			trigger(n.Scope, EvNetworkClosing)
		})
		n.waitClose.Wait()
	})
}
