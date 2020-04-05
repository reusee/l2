package l2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

type UDPLocal struct {
	Conn      *net.UDPConn
	Port      int
	StartedAt time.Time
}

type UDPRemote struct {
	UDPAddr    *net.UDPAddr
	UDPAddrStr string
	AddedAt    time.Time
	IPs        []net.IP
	Addrs      []net.HardwareAddr
}

type UDPInbound struct {
	RemoteAddr *net.UDPAddr
	Inbound    *Inbound
	LocalPort  int
}

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
	trigger Trigger,
	bridgeIndex BridgeIndex,
	localAddrs []net.Addr,
	ifaceAddr net.HardwareAddr,
) {

	portShiftInterval := time.Millisecond * 8311
	localConnDuration := portShiftInterval * 2
	remoteDuration := portShiftInterval * 3

	getPort := shiftingPort(
		fmt.Sprintf("%x-udp-", network.CryptoKey),
		portShiftInterval,
	)

	var remotes []*UDPRemote
	var locals []*UDPLocal

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
			now := getTime()
			port := getPort(node, now)
			udpAddr := &net.UDPAddr{
				IP:   ip,
				Port: port,
			}
			udpAddrStr := udpAddr.String()
			for _, remote := range remotes {
				if remote.UDPAddrStr == udpAddrStr {
					continue loop_nodes
				}
			}

			remote := &UDPRemote{
				UDPAddr:    udpAddr,
				UDPAddrStr: udpAddrStr,
				AddedAt:    now,
				IPs: []net.IP{
					node.LanIP,
				},
			}
			remotes = append(remotes, remote)
			trigger(scope.Sub(
				&remote,
			), EvUDP, EvUDPRemoteAdded)
		}

	}
	updateRemotes()

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
		local := &UDPLocal{
			Conn:      conn,
			Port:      port,
			StartedAt: now,
		}
		locals = append(locals, local)
		trigger(scope.Sub(
			&local,
		), EvUDP, EvUDPLocalAdded)

		spawn(scope, func() {
			bs := make([]byte, network.MTU*2)
			for {
				n, remoteAddr, err := conn.ReadFromUDP(bs)
				if err != nil {
					trigger(scope.Sub(
						&local, &err,
					), EvUDP, EvUDPConnReadError)
					return
				}
				r := bytes.NewReader(bs[:n])
				for {
					var length uint16
					if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
						break
					}
					inbound, err := network.readInbound(
						&io.LimitedReader{
							R: r,
							N: int64(length),
						},
					)
					if err != nil {
						trigger(scope.Sub(
							&local, &err,
						), EvUDP, EvUDPReadInboundError)
						return
					}
					inbounds <- UDPInbound{
						RemoteAddr: remoteAddr,
						Inbound:    inbound,
						LocalPort:  port,
					}
				}

			}
		})

	}

	port := getPort(network.LocalNode, getTime().Add(time.Second))
	addLocal(port)

	close(ready)
	trigger(scope, EvUDP, EvUDPReady)

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

	type queueK struct {
		remote *UDPRemote
	}

	queue := newSendQueue(
		network,
		func(ip *net.IP, addr *net.HardwareAddr, data []byte) {
			sent := false

			var remote *UDPRemote
			// select remote
			for i := len(remotes) - 1; i >= 0; i-- {
				remote = remotes[i]

				skip := false
				ipMatched := false
				addrMatched := false
				if len(remote.Addrs) == 0 && len(remote.IPs) == 0 {
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
				if addr != nil && len(remote.Addrs) > 0 {
					ok := false
					for _, remoteAddr := range remote.Addrs {
						if bytes.Equal(remoteAddr, *addr) {
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

				if ipMatched || addrMatched {
					break
				}

			}

			if remote == nil {
				trigger(scope.Sub(
					&remotes,
				), EvUDP, EvUDPNotSent)
				return
			}

			// send
			for i := len(locals) - 1; i >= 0; i-- {
				local := locals[i]
				_, err := local.Conn.WriteToUDP(data, remote.UDPAddr)
				if err != nil {
					trigger(scope.Sub(
						&local, &remote,
					), EvUDP, EvUDPWriteError)
					continue
				}
				sent = true
				break
			}
			if !sent {
				trigger(scope.Sub(
					&remote, &remotes,
				), EvUDP, EvUDPNotSent)
			}

		},
	)

	for {
		select {

		case <-refreshConnsTicker.C:
			now := getTime()
			// add local conn
			port := getPort(network.LocalNode, getTime().Add(time.Second))
			addLocal(port)
			// delete local conn
			for i := 0; i < len(locals); {
				local := locals[i]
				if now.Sub(local.StartedAt) > localConnDuration {
					local.Conn.Close()
					locals = append(locals[:i], locals[i+1:]...)
					trigger(scope.Sub(
						&local,
					), EvUDP, EvUDPLocalClosed)
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
					trigger(scope.Sub(
						&remote,
					), EvUDP, EvUDPRemoteClosed)
					continue
				}
				i++
			}

		case inbound := <-inbounds:
			addrStr := inbound.RemoteAddr.String()
			now := getTime()

			var remote *UDPRemote
			for _, r := range remotes {
				if r.UDPAddrStr == addrStr {
					remote = r
					break
				}
			}
			if remote == nil {
				remote = &UDPRemote{
					UDPAddr:    inbound.RemoteAddr,
					UDPAddrStr: addrStr,
					AddedAt:    now,
				}
				remotes = append(remotes, remote)
				trigger(scope.Sub(
					&remote,
				), EvUDP, EvUDPRemoteAdded)
			}

			if len(remote.Addrs) == 0 || len(remote.IPs) == 0 {
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
						if !bytes.Equal(eth.SrcMAC, EthernetBroadcast) {
							addr := make(net.HardwareAddr, len(eth.SrcMAC))
							copy(addr, eth.SrcMAC)
							for _, a := range remote.Addrs {
								if bytes.Equal(a, addr) {
									break s
								}
							}
							remote.Addrs = append(remote.Addrs, addr)
							trigger(scope.Sub(
								&remote, &addr,
							), EvUDP, EvUDPRemoteGotAddr)
						}

					case layers.LayerTypeARP:
						if !IPv4zero.Equal(arp.SourceProtAddress) {
							ip := make(net.IP, len(arp.SourceProtAddress))
							copy(ip, arp.SourceProtAddress)
							for _, i := range remote.IPs {
								if i.Equal(ip) {
									break s
								}
							}
							remote.IPs = append(remote.IPs, ip)
							trigger(scope.Sub(
								&remote, &ip,
							), EvUDP, EvUDPRemoteGotIP)
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
							trigger(scope.Sub(
								&remote, &ip,
							), EvUDP, EvUDPRemoteGotIP)
						}

					}
				}
			}

			inbound.Inbound.BridgeIndex = uint8(bridgeIndex)
			select {
			case inboundCh <- inbound.Inbound:
				trigger(scope.Sub(
					&inbound, &inbound.Inbound,
				), EvUDP, EvUDPInboundSent)
			case <-closing:
			}

		case outbound := <-outboundCh:
			if outbound == nil {
				break
			}
			queue.enqueue(outbound)

		case <-queue.timer.C:
			queue.tick()

		case <-closing:
			for _, local := range locals {
				local.Conn.Close()
			}
			trigger(scope, EvUDP, EvUDPClosed)
			return

		}
	}

}
