package l2

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"golang.org/x/net/icmp"
	netipv4 "golang.org/x/net/ipv4"
)

type ICMPRemote struct {
	ID  uint16
	IP  net.IP
	IPs []net.IP
}

type ICMPInbound struct {
	Addr    *net.IPAddr
	ID      uint16
	Inbound *Inbound
}

type StartICMP func(
	bridgeIndex int,
	inboundChan chan *Inbound,
	outboundChan chan *Outbound,
	ready chan struct{},
	inboundSenderGroup *sync.WaitGroup,
)

func (n *Network) StartICMP(
	spawn Spawn,
	closing Closing,
	trigger Trigger,
	sysAddrs SystemInterfaceAddrs,
	localNode *Node,
	mtu MTU,
	readInbound ReadInbound,
	newSendQueue NewSendQueue,
	allNodes AllNodes,
	getReachableIP GetReachableIP,
) StartICMP {

	return func(
		bridgeIndex int,
		inboundChan chan *Inbound,
		outboundChan chan *Outbound,
		ready chan struct{},
		_ *sync.WaitGroup,
	) {

		scope := n.RootScope

		// remotes
		var remotes []*ICMPRemote
		for _, node := range allNodes {

			if !node.HasBridge(BridgeICMP) {
				continue
			}
			if node == localNode {
				continue
			}
			ip := getReachableIP(node)
			if len(ip) == 0 {
				continue
			}

			node := node
			remote := &ICMPRemote{
				IP: ip,
				IPs: []net.IP{
					node.LanIP,
				},
			}
			remotes = append(remotes, remote)
		}

		inbounds := make(chan ICMPInbound, 1024)

		// local
		var localConn *net.IPConn
		localConnOK := make(chan struct{})
		spawn(func() {
			node := localNode
			ip := getReachableIP(node)
			if len(ip) == 0 {
				return
			}

			var err error
			localConn, err = net.ListenIP("ip4:icmp", &net.IPAddr{
				IP: net.ParseIP("0.0.0.0"),
			})
			ce(err)
			close(localConnOK)

			buf := make([]byte, mtu*2)
			for {
				n, addr, err := localConn.ReadFrom(buf)
				ce(err)
				payload := buf[:n]
				if payload[0] != 0 && payload[0] != 8 {
					// not echo
					continue
				}
				id := binary.BigEndian.Uint16(payload[4:6])
				bs := payload[8:]

				r := bytes.NewReader(bs)
				for {
					var length uint16
					if err := binary.Read(r, binary.LittleEndian, &length); is(err, io.EOF) {
						break
					} else if err != nil {
						trigger(scope.Fork(
							&localConn, &err,
						), EvUDP, EvICMPReadInboundError)
						break
					}
					if length == 0 {
						break
					}
					inbound, err := readInbound(
						&io.LimitedReader{
							R: r,
							N: int64(length),
						},
					)
					if err != nil {
						trigger(scope.Fork(
							&localConn, &err,
						), EvUDP, EvICMPReadInboundError)
						break
					}
					inbounds <- ICMPInbound{
						Addr:    addr.(*net.IPAddr),
						ID:      id,
						Inbound: inbound,
					}
				}

			}
		})

		close(ready)
		trigger(scope, EvICMP, EvICMPReady)

		parser := gopacket.NewDecodingLayerParser(layers.LayerTypeEthernet)
		parser.SetDecodingLayerContainer(gopacket.DecodingLayerSparse(nil))
		var eth layers.Ethernet
		var ipv4 layers.IPv4
		parser.AddDecodingLayer(&eth)
		parser.AddDecodingLayer(&ipv4)
		decoded := make([]gopacket.LayerType, 0, 10)

		seq := 42
		var msgType icmp.Type
		if len(localNode.wanIP) > 0 {
			msgType = netipv4.ICMPTypeEchoReply
		} else {
			msgType = netipv4.ICMPTypeEcho
		}

		queue := newSendQueue(
			func(ip *net.IP, _ *net.HardwareAddr, data []byte) {
				sent := false

				// pick a icmp remote
				var r *ICMPRemote
				for _, remote := range remotes {
					skip := false
					ipMatched := false
					if len(remote.IPs) == 0 {
						skip = true
					}
					if ip != nil && len(remote.IPs) > 0 {
						ok := false
						for _, remoteIP := range remote.IPs {
							if remoteIP.Equal(*ip) {
								ok = true
								break
							}
						}
						if !ok {
							skip = true
						} else {
							ipMatched = true
						}
					}
					if skip {
						continue
					}

					if ipMatched {
						r = remote
						break
					}
				}

				if r == nil {
					trigger(scope.Fork(
						&remotes,
					), EvICMP, EvICMPNotSent)
					return
				}

				// sent
				seq++
				msg := &icmp.Message{
					Type: msgType,
					Body: &icmp.Echo{
						ID:   int(r.ID),
						Seq:  seq,
						Data: data,
					},
				}
				payload, err := msg.Marshal(nil)
				ce(err)
				<-localConnOK
				if _, err := localConn.WriteTo(payload, &net.IPAddr{
					IP: r.IP,
				}); err != nil {
					trigger(scope.Fork(
						&r,
					), EvICMP, EvICMPWriteError)
					return
				}

				if !sent {
					trigger(scope.Fork(
						&r, &remotes,
					), EvICMP, EvICMPNotSent)
				}
			},
		)

		for {
			select {

			case inbound := <-inbounds:
				var remote *ICMPRemote
				for _, r := range remotes {
					if r.IP.Equal(inbound.Addr.IP) {
						remote = r
						break
					}
				}
				if remote == nil {
					remote = &ICMPRemote{
						IP: inbound.Addr.IP,
					}
					remotes = append(remotes, remote)
				}

				remote.ID = inbound.ID

				// update remote IPs
				if len(remote.IPs) == 0 {
					parser.DecodeLayers(inbound.Inbound.Eth, &decoded)
					for _, t := range decoded {
					s:
						switch t {

						case layers.LayerTypeEthernet:
							if !bytes.Equal(eth.DstMAC, EthernetBroadcast) {
								destAddr := make(net.HardwareAddr, len(eth.DstMAC))
								copy(destAddr, eth.DstMAC)
								inbound.Inbound.DestAddr = &destAddr
							}

						case layers.LayerTypeIPv4:
							if !IPv4zero.Equal(ipv4.SrcIP) {
								ip := make(net.IP, len(ipv4.SrcIP))
								copy(ip, ipv4.SrcIP)
								for _, i := range remote.IPs {
									if i.Equal(ip) {
										break s
									}
								}
								remote.IPs = append(remote.IPs, ip)
							}

						}
					}
				}

				inbound.Inbound.BridgeIndex = uint8(bridgeIndex)
				select {
				case inboundChan <- inbound.Inbound:
					trigger(scope.Fork(
						&remote, &inbound,
					), EvICMP, EvICMPInboundSent)
				case <-closing:
				}

			case <-closing:
				if localConn != nil {
					localConn.Close()
				}
				trigger(scope, EvICMP, EvICMPClosed)
				return

			case outbound := <-outboundChan:
				if outbound == nil {
					break
				}
				queue.enqueue(outbound)

			case <-queue.timer.C:
				queue.tick()

			}
		}

	}

}
