package l2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/reusee/dscope"
)

type Interface interface {
	io.ReadWriteCloser
	Name() string
}

type Network struct {
	RootScope Scope
}

var (
	EthernetBroadcast = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	IPv4zero          = net.IPv4(0, 0, 0, 0)
)

func NewNetwork(parent Scope, defSets ...[]any) *Network {
	network := new(Network)
	scope := parent.Fork(dscope.Methods(network)...)
	for _, set := range defSets {
		scope = scope.Fork(set...)
	}
	network.RootScope = scope
	return network
}

type Start func() (err error)

func (n *Network) Start(
	localNode *Node,
	startTCP StartTCP,
	startUDP StartUDP,
	startICMP StartICMP,
	spawn Spawn,
	on On,
	closing Closing,
	mtu MTU,
	preferFormatFunc PreferFormatFunc,
	iface Interface,
	ipnet net.IPNet,
	onFrameFunc OnFrameFunc,
	cryptoKey CryptoKey,
	cryptoKeyInt CryptoKeyInt,
	trigger Trigger,
	hwAddr HardwareAddr,
) Start {

	scope := n.RootScope

	return func() (err error) {
		defer he(&err)

		var outboundChans []chan *Outbound
		inboundChan := make(chan *Inbound, 1024)

		// start bridges
		inboundSenderGroup := new(sync.WaitGroup)
		for i, name := range localNode.BridgeNames {
			i := i
			var startFunc func(
				bridgeIndex int,
				inboundChan chan *Inbound,
				outboundChan chan *Outbound,
				ready chan struct{},
				inboundSenderGroup *sync.WaitGroup,
			)
			switch name {
			case "TCP":
				startFunc = startTCP
			case "UDP":
				startFunc = startUDP
			case "ICMP":
				startFunc = startICMP
			default:
				panic(fmt.Errorf("unknown bridge name: %s", name))
			}
			outboundChan := make(chan *Outbound, 1024)
			outboundChans = append(outboundChans, outboundChan)
			ready := make(chan struct{})
			spawn(func() {
				startFunc(
					i,
					inboundChan,
					outboundChan,
					ready,
					inboundSenderGroup,
				)
			})
			<-ready
		}

		on(EvNetworkClosing, func() {
			inboundSenderGroup.Wait()
		})

		// workers
		jobs := make(chan func(), 1024)
		for i := 0; i < runtime.NumCPU(); i++ {
			spawn(func() {
				for {
					select {
					case <-closing:
						return
					case fn := <-jobs:
						fn()
					}
				}
			})
		}

		outboundSenderGroup := new(sync.WaitGroup)
		outboundSenderGroup.Add(1)

		type ActiveKey struct {
			Addr        [6]byte
			BridgeIndex uint8
		}
		lastActive := make(map[ActiveKey]time.Time)
		lastActiveL := new(sync.RWMutex)

		// interface -> bridge
		spawn(func() {
			defer outboundSenderGroup.Done()

			buf := make([]byte, int(mtu)+14)
			parser := gopacket.NewDecodingLayerParser(layers.LayerTypeEthernet)
			parser.SetDecodingLayerContainer(gopacket.DecodingLayerSparse(nil))
			var eth layers.Ethernet
			var arp layers.ARP
			var ipv4 layers.IPv4
			var tcp layers.TCP
			var udp layers.UDP
			parser.AddDecodingLayer(&eth)
			parser.AddDecodingLayer(&ipv4)
			parser.AddDecodingLayer(&arp)
			parser.AddDecodingLayer(&tcp)
			parser.AddDecodingLayer(&udp)
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
						ce.WithInfo("read from interface")(err)
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
						if !ipnet.Contains(ipv4.DstIP) {
							continue loop
						}
						// dest ip
						ip := make(net.IP, len(ipv4.DstIP))
						copy(ip, ipv4.DstIP)
						destIP = &ip

					}
				}

				if onFrameFunc != nil {
					onFrameFunc(bs)
				}

				sn := atomic.AddUint64(&serial, 1)

				ethBytes := make([]byte, l)
				copy(ethBytes, bs)

				preferFormat := WireFormat(0) // default
				if preferFormatFunc != nil {
					format := preferFormatFunc(
						&eth, &arp, &ipv4, &tcp, &udp, &decoded,
					)
					if format != preferFormat {
						preferFormat = format
					}
				}

				outbound := &Outbound{
					WireData: WireData{
						Eth:    ethBytes,
						Serial: sn,
					},
					DestIP:       destIP,
					DestAddr:     destAddr,
					PreferFormat: preferFormat,
				}
				jobs <- func() {
					ce(outbound.encode(cryptoKey, cryptoKeyInt))
				}

				// select bridge
				/*
					sent := false
					if outbound.DestAddr != nil {
						var addr [6]byte
						copy(addr[:], *outbound.DestAddr)
					loop_bridges:
						for i := 0; i < len(outboundChans); i++ {
							key := ActiveKey{
								Addr:        addr,
								BridgeIndex: uint8(i),
							}
							lastActiveL.RLock()
							last, ok := lastActive[key]
							lastActiveL.RUnlock()
							if ok && time.Since(last) < time.Second {
								select {
								case outboundChans[i] <- outbound:
									sent = true
									break loop_bridges
								case <-closing:
								}
							}
						}
					}
					if !sent {
						for _, ch := range outboundChans {
							select {
							case ch <- outbound:
							case <-closing:
							}
						}
					}
				*/

				for _, ch := range outboundChans {
					select {
					case ch <- outbound:
					case <-closing:
					}
				}

				//TODO
				//trigger(scope.Fork(
				//	&outbound,
				//), EvNetwork, EvNetworkOutboundSent)

			}
		})

		// bridge -> interface
		doChan := make(chan func(), 1024)
		spawn(func() {
			for {
				select {
				case <-closing:
					return
				case fn := <-doChan:
					fn()
				}
			}
		})

		on(EvNetworkClosing, func() {
			iface.Close()
			outboundSenderGroup.Wait()
		})

		// interface <- bridge
		injectFrame := make(chan []byte, 1024)

		spawn(func() {

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

					trigger(scope.Fork(
						&inbound,
					), EvNetwork, EvNetworkInboundReceived)

					if len(inbound.Eth) == 0 {
						break
					}

					parser.DecodeLayers(inbound.Eth, &decoded)

					var fromAddr [6]byte
					for _, t := range decoded {
						switch t {

						case layers.LayerTypeEthernet:
							// de-duplicate
							if inbound.Serial > 0 {
								copy(macBytes, eth.SrcMAC)
								macInt := binary.LittleEndian.Uint64(macBytes)
								m, ok := dedup[macInt]
								if !ok {
									m = make([]uint64, 1<<17)
									dedup[macInt] = m
								}
								if m[inbound.Serial%(1<<17)] == inbound.Serial {
									trigger(scope.Fork(
										&inbound,
									), EvNetwork, EvNetworkInboundDuplicated)
									continue loop_inbound
								}
								m[inbound.Serial%(1<<17)] = inbound.Serial
							}
							copy(fromAddr[:], eth.SrcMAC)

						}
					}

					if inbound.DestAddr != nil &&
						!bytes.Equal(*inbound.DestAddr, EthernetBroadcast) &&
						len(hwAddr) > 0 &&
						!bytes.Equal(*inbound.DestAddr, hwAddr) {
						break
					}
					doChan <- func() {
						_, err := iface.Write(inbound.Eth)
						if err != nil {
							return
						}
						trigger(scope.Fork(
							&inbound,
						), EvNetwork, EvNetworkInboundWritten)
						key := ActiveKey{
							Addr:        fromAddr,
							BridgeIndex: inbound.BridgeIndex,
						}
						lastActiveL.Lock()
						lastActive[key] = time.Now()
						lastActiveL.Unlock()
					}

				case bs := <-injectFrame:
					_, _ = iface.Write(bs)

				case <-closing:
					return

				}
			}
		})

		return nil

	}
}
