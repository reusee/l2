package l2

import (
	"bytes"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func startUDP(
	ready Ready,
	scope Scope,
	spawn Spawn,
	closing Closing,
	outboundCh chan *Outbound,
	network *Network,
	getTime func() time.Time,
	inboundCh chan *Inbound,
	inboundSenderGroup *sync.WaitGroup,
) {

	portShiftInterval := time.Second * 5
	localConnDuration := portShiftInterval * 2
	remoteDuration := portShiftInterval * 3

	getPort := shiftingPort(
		fmt.Sprintf("%x-udp-", network.CryptoKey),
		portShiftInterval,
	)

	type Local struct {
		Conn      *net.UDPConn
		Port      int
		StartedAt time.Time
	}

	type Remote struct {
		UDPAddr    *net.UDPAddr
		UDPAddrStr string
		AddedAt    time.Time
		IPs        []net.IP
		Addrs      []net.HardwareAddr
	}

	var remotes []*Remote
	var locals []*Local

	updateRemotes := func() {
	loop_nodes:
		for _, node := range network.nodes.Load().([]*Node) {
			hasUDP := false
			for _, name := range node.BridgeNames {
				if name == "UDP" {
					hasUDP = true
					break
				}
			}
			if !hasUDP {
				continue
			}
			if node == network.LocalNode {
				// non remote
				continue
			}
			node := node
			now := getTime()
			remotePort := getPort(node, now)
			udpAddr := &net.UDPAddr{
				IP:   node.wanIP,
				Port: remotePort,
			}
			udpAddrStr := udpAddr.String()
			for _, remote := range remotes {
				if remote.UDPAddrStr == udpAddrStr {
					continue loop_nodes
				}
			}
			remotes = append(remotes, &Remote{
				UDPAddr:    udpAddr,
				UDPAddrStr: udpAddrStr,
				AddedAt:    now,
				IPs: []net.IP{
					node.LanIP,
				},
			})
		}
	}
	updateRemotes()

	type UDPInbound struct {
		RemoteAddr *net.UDPAddr
		Inbound    *Inbound
		LocalPort  int
	}
	inbounds := make(chan UDPInbound, 1024)

	addLocal := func(port int) {
		for _, local := range locals {
			if local.Port == port {
				return
			}
		}
		udpAddr := &net.UDPAddr{
			IP:   net.IPv4(0, 0, 0, 0),
			Port: port,
		}
		conn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			return
		}
		now := getTime()
		locals = append(locals, &Local{
			Conn:      conn,
			Port:      port,
			StartedAt: now,
		})

		spawn(scope, func() {
			bs := make([]byte, network.MTU*2)
			for {
				n, remoteAddr, err := conn.ReadFromUDP(bs)
				if err != nil {
					return
				}
				inbound, err := network.readInbound(bytes.NewReader(bs[:n]))
				if err != nil {
					return
				}
				inbounds <- UDPInbound{
					RemoteAddr: remoteAddr,
					Inbound:    &inbound,
					LocalPort:  port,
				}
			}
		})

	}
	addLocal(getPort(network.LocalNode, getTime().Add(time.Second)))

	close(ready)

	refreshConnsTicker := time.NewTicker(time.Second * 1)

	parser := gopacket.NewDecodingLayerParser(layers.LayerTypeEthernet)
	parser.SetDecodingLayerContainer(gopacket.DecodingLayerSparse(nil))
	var eth layers.Ethernet
	var arp layers.ARP
	var ipv4 layers.IPv4
	parser.AddDecodingLayer(&eth)
	parser.AddDecodingLayer(&ipv4)
	parser.AddDecodingLayer(&arp)
	decoded := make([]gopacket.LayerType, 0, 10)

	for {
		select {

		case <-refreshConnsTicker.C:
			now := getTime()
			// add local conn
			addLocal(getPort(network.LocalNode, getTime().Add(time.Second)))
			// delete local conn
			for i := 0; i < len(locals); {
				local := locals[i]
				if now.Sub(local.StartedAt) > localConnDuration {
					local.Conn.Close()
					locals = append(locals[:i], locals[i+1:]...)
					continue
				}
				i++
			}
			// add remote
			updateRemotes()
			// delete remote
			for i := 0; i < len(remotes); {
				remote := remotes[i]
				if now.Sub(remote.AddedAt) > remoteDuration {
					remotes = append(remotes[:i], remotes[i+1:]...)
					continue
				}
				i++
			}

		case inbound := <-inbounds:
			addrStr := inbound.RemoteAddr.String()
			now := getTime()

			var remote *Remote
			for _, r := range remotes {
				if r.UDPAddrStr == addrStr {
					remote = r
					break
				}
			}
			if remote == nil {
				remote = &Remote{
					UDPAddr:    inbound.RemoteAddr,
					UDPAddrStr: addrStr,
					AddedAt:    now,
				}
				remotes = append(remotes, remote)
			}

			if len(remote.Addrs) == 0 || len(remote.IPs) == 0 {
				parser.DecodeLayers(inbound.Inbound.Eth, &decoded)
				for _, t := range decoded {
					switch t {

					case layers.LayerTypeEthernet:
						if !bytes.Equal(eth.SrcMAC, EthernetBroadcast) {
							addr := make(net.HardwareAddr, len(eth.SrcMAC))
							copy(addr, eth.SrcMAC)
							remote.Addrs = append(remote.Addrs, addr)
						}

					case layers.LayerTypeARP:
						ip := make(net.IP, len(arp.SourceProtAddress))
						copy(ip, arp.SourceProtAddress)
						remote.IPs = append(remote.IPs, ip)

					case layers.LayerTypeIPv4:
						ip := make(net.IP, len(ipv4.SrcIP))
						copy(ip, ipv4.SrcIP)
						remote.IPs = append(remote.IPs, ip)

					}
				}
			}

			select {
			case inboundCh <- inbound.Inbound:
			case <-closing:
			}

		case outbound := <-outboundCh:
			if outbound == nil {
				break
			}

			sent := false

			for i := len(remotes) - 1; i >= 0; i-- {
				remote := remotes[i]

				// filter
				skip := false
				ipMatched := false
				addrMatched := false
				if outbound.DestIP != nil && len(remote.IPs) > 0 {
					ok := false
					for _, ip := range remote.IPs {
						if ip.Equal(*outbound.DestIP) {
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
				if outbound.DestAddr != nil && len(remote.Addrs) > 0 {
					ok := false
					for _, addr := range remote.Addrs {
						if bytes.Equal(addr, *outbound.DestAddr) {
							ok = true
							break
						}
					}
					if !ok {
						skip = true
					} else {
						addrMatched = true
					}
				}
				if skip {
					continue
				}

				// send
				if len(locals) > 0 {
					local := locals[len(locals)-1]
					buf := new(bytes.Buffer)
					ce(network.writeOutbound(buf, outbound))
					_, err := local.Conn.WriteToUDP(buf.Bytes(), remote.UDPAddr)
					if err != nil {
						continue
					}
					sent = true
				}

				if ipMatched || addrMatched {
					break
				}

			}

			if !sent {
				//pt("--- not sent ---\n")
				//pt("serial %d\n", outbound.Serial)
				//dumpEth(outbound.Eth)
			}

		case <-closing:
			for _, local := range locals {
				local.Conn.Close()
			}
			return

		}
	}

}
