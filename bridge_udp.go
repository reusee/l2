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

type StartUDP func(
	bridgeIndex int,
	inboundChan chan *Inbound,
	outboundChan chan *Outbound,
	ready chan struct{},
	inboundSenderGroup *sync.WaitGroup,
)

func (n *Network) StartUDP(
	spawn Spawn,
	closing Closing,
	getTime GetTime,
	trigger Trigger,
	sysAddrs SystemInterfaceAddrs,
	localNode *Node,
	mtu MTU,
	readInbound ReadInbound,
	key CryptoKey,
	newSendQueue NewSendQueue,
	allNodes AllNodes,
	getReachableIP GetReachableIP,
) StartUDP {

	return func(
		bridgeIndex int,
		inboundChan chan *Inbound,
		outboundChan chan *Outbound,
		ready chan struct{},
		_ *sync.WaitGroup,
	) {

		scope := n.RootScope

		portShiftInterval := time.Millisecond * 8311
		localConnDuration := portShiftInterval * 2
		remoteDuration := portShiftInterval * 4

		getPort := shiftingPort(
			fmt.Sprintf("%x-udp-", key),
			portShiftInterval,
		)

		var remotes []*UDPRemote
		var locals []*UDPLocal

		updateRemotes := func() {
		loop_nodes:
			for _, node := range allNodes {
				if !node.HasBridge(BridgeUDP) {
					continue
				}
				if node == localNode {
					// non remote
					continue
				}
				ip := getReachableIP(node)
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
				trigger(scope.Fork(
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
			trigger(scope.Fork(
				&local,
			), EvUDP, EvUDPLocalAdded)

			spawn(func() {
				bs := make([]byte, int(mtu)*2)
				for {
					n, remoteAddr, err := conn.ReadFromUDP(bs)
					if err != nil {
						trigger(scope.Fork(
							&local, &err,
						), EvUDP, EvUDPConnReadError)
						return
					}
					r := bytes.NewReader(bs[:n])
					for {
						var length uint16
						if err := binary.Read(r, binary.LittleEndian, &length); is(err, io.EOF) {
							break
						} else if err != nil {
							trigger(scope.Fork(
								&local, &err,
							), EvUDP, EvUDPReadInboundError)
							return
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

		port := getPort(localNode, getTime())
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

		queue := newSendQueue(
			func(ip *net.IP, addr *net.HardwareAddr, data []byte) {
				sent := false

				var r *UDPRemote
				// select remote
				for i := len(remotes) - 1; i >= 0; i-- {
					remote := remotes[i]

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
						r = remote
						break
					}

				}

				if r == nil {
					trigger(scope.Fork(
						&remotes,
					), EvUDP, EvUDPNotSent)
					return
				}

				// send
				for i := len(locals) - 1; i >= 0; i-- {
					local := locals[i]
					local.Conn.SetWriteDeadline(time.Now().Add(time.Second * 2))
					_, err := local.Conn.WriteToUDP(data, r.UDPAddr)
					if err != nil {
						trigger(scope.Fork(
							&local, &r,
						), EvUDP, EvUDPWriteError)
						continue
					}
					sent = true
					break
				}
				if !sent {
					trigger(scope.Fork(
						&r, &remotes,
					), EvUDP, EvUDPNotSent)
				}

			},
		)

		for {
			select {

			case <-refreshConnsTicker.C:
				now := getTime()
				// add local conn
				port := getPort(localNode, getTime())
				addLocal(port)
				// delete local conn
				for i := 0; i < len(locals); {
					local := locals[i]
					if now.Sub(local.StartedAt) > localConnDuration {
						local.Conn.Close()
						locals = append(locals[:i], locals[i+1:]...)
						trigger(scope.Fork(
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
						trigger(scope.Fork(
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
					trigger(scope.Fork(
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
								trigger(scope.Fork(
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
								trigger(scope.Fork(
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
								trigger(scope.Fork(
									&remote, &ip,
								), EvUDP, EvUDPRemoteGotIP)
							}

						}
					}
				}

				inbound.Inbound.BridgeIndex = uint8(bridgeIndex)
				select {
				case inboundChan <- inbound.Inbound:
					trigger(scope.Fork(
						&inbound, &inbound.Inbound,
					), EvUDP, EvUDPInboundSent)
				case <-closing:
				}

			case outbound := <-outboundChan:
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

}
