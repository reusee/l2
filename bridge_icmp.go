package l2

import (
	"bytes"
	"encoding/binary"
	"net"

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

func startICMP(
	ready Ready,
	scope Scope,
	spawn Spawn,
	closing Closing,
	outboundCh chan *Outbound,
	network *Network,
	inboundCh chan *Inbound,
	trigger Trigger,
	bridgeIndex BridgeIndex,
	localAddrs []net.Addr,
) {

	// remotes
	var remotes []*ICMPRemote
	for _, node := range network.nodes.Load().([]*Node) {
		hasICMP := false
		for _, name := range node.BridgeNames {
			if name == "ICMP" {
				hasICMP = true
				break
			}
		}
		if !hasICMP {
			continue
		}

		if node == network.LocalNode {
			continue
		}

		ip := node.wanIP
		if len(ip) == 0 && len(node.PrivateIP) > 0 {
			for _, addr := range localAddrs {
				if ipnet, ok := addr.(*net.IPNet); ok && ipnet.Contains(node.PrivateIP) {
					ip = node.PrivateIP
					break
				}
			}
		}
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
	node := network.LocalNode
	ip := node.wanIP
	if len(ip) == 0 && len(node.PrivateIP) > 0 {
		for _, addr := range localAddrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.Contains(node.PrivateIP) {
				ip = node.PrivateIP
				break
			}
		}
	}
	if len(ip) == 0 {
		return
	}
	localConn, err := net.ListenIP("ip4:icmp", &net.IPAddr{
		IP: net.ParseIP("0.0.0.0"),
	})
	ce(err)
	spawn(scope, func() {
		buf := make([]byte, network.MTU*2)
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
			inbound, err := network.readInbound(bytes.NewReader(bs))
			if err != nil {
				continue
			}
			inbounds <- ICMPInbound{
				Addr:    addr.(*net.IPAddr),
				ID:      id,
				Inbound: inbound,
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
	if len(network.LocalNode.wanIP) > 0 {
		msgType = netipv4.ICMPTypeEchoReply
	} else {
		msgType = netipv4.ICMPTypeEcho
	}

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
			case inboundCh <- inbound.Inbound:
				trigger(scope.Sub(
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

		case outbound := <-outboundCh:
			if outbound == nil {
				break
			}

			sent := false
			var r *ICMPRemote
			for _, remote := range remotes {
				skip := false
				ipMatched := false
				addrMatched := false
				if len(remote.IPs) == 0 {
					skip = true
				}
				ip := outbound.DestIP
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

				if ipMatched || addrMatched {
					r = remote
					break
				}
			}

			if r == nil {
				trigger(scope.Sub(
					&remotes,
				), EvICMP, EvICMPNotSent)
				break
			}

			// sent
			buf := new(bytes.Buffer)
			ce(network.writeOutbound(buf, outbound))
			bs := make([]byte, 4+buf.Len())
			seq++
			msg := &icmp.Message{
				Type: msgType,
				Body: &EchoBody{
					Echo: &icmp.Echo{
						ID:   int(r.ID),
						Seq:  seq,
						Data: buf.Bytes(),
					},
					b: bs,
				},
			}
			payload, err := msg.Marshal(nil)
			ce(err)
			if _, err := localConn.WriteTo(payload, &net.IPAddr{
				IP: r.IP,
			}); err != nil {
			}

			if !sent {
				trigger(scope.Sub(
					&r, &remotes,
				), EvICMP, EvICMPNotSent)
			}

		}
	}

}

type EchoBody struct {
	*icmp.Echo
	b []byte
}

func (p *EchoBody) Marshal(proto int) ([]byte, error) {
	binary.BigEndian.PutUint16(p.b[:2], uint16(p.ID))
	binary.BigEndian.PutUint16(p.b[2:4], uint16(p.Seq))
	copy(p.b[4:], p.Data)
	return p.b, nil
}
