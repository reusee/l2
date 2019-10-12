package l2

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

type TCPListener struct {
	Listener  net.Listener
	StartedAt time.Time
}

type TCPConn struct {
	sync.RWMutex
	*net.TCPConn
	IPs   []net.IP
	Addrs []net.HardwareAddr
}

func startTCP(
	ready Ready,
	scope Scope,
	network *Network,
	closing Closing,
	spawn Spawn,
	outboundCh chan *Outbound,
	inboundCh chan *Inbound,
	inboundSenderGroup *sync.WaitGroup,
	getTime func() time.Time,
	trigger Trigger,
	bridgeIndex BridgeIndex,
) {

	// port
	portShiftInterval := time.Millisecond * 5113
	listenerDuration := portShiftInterval * 2
	connDuration := portShiftInterval * 3
	getPort := shiftingPort(
		fmt.Sprintf("%x-tcp-", network.CryptoKey),
		portShiftInterval,
	)

	// listener
	listeners := make(map[string]*TCPListener)

	// conn
	var conns []*TCPConn

	// sync callback
	doInLoopCh := make(chan func(), 1024)
	doInLoop := func(fn func()) {
		select {
		case doInLoopCh <- fn:
		case <-closing:
		}
	}

	// conn funcs
	addConn := func(conn *TCPConn) {
		doInLoop(func() {
			// greeting packet
			if err := network.writeOutbound(conn, &Outbound{
				WireData: WireData{
					IP: network.LocalNode.LanIP,
				},
			}); err != nil {
				conn.CloseWrite()
				return
			}
			conns = append(conns, conn)
			trigger(scope.Sub(
				&conn,
			), EvTCP, EvTCPConnAdded)
		})
	}
	deleteConn := func(conn *TCPConn) {
		doInLoop(func() {
			// delete conn from conns
			for i := 0; i < len(conns); {
				if conns[i] == conn {
					conns = append(conns[:i], conns[i+1:]...)
					trigger(scope.Sub(
						&conn,
					), EvTCP, EvTCPConnDeleted)
					continue
				}
				i++
			}
		})
	}

	readConn := func(conn *TCPConn) {
		inboundSenderGroup.Add(1)
		defer inboundSenderGroup.Done()

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
			inbound, err := network.readInbound(conn)

			if err != nil {
				trigger(scope.Sub(
					&conn, &err,
				), EvTCP, EvTCPReadInboundError)
				conn.CloseRead()
				select {
				case <-closing:
				default:
					deleteConn(conn)
				}
				return
			}

			conn.RLock()
			if inbound.IP != nil {
				conn.RUnlock()
				conn.Lock()
				conn.IPs = append(conn.IPs, inbound.IP)
				conn.Unlock()
				conn.RLock()
			}
			if (len(conn.Addrs) == 0 || len(conn.IPs) == 0) && len(inbound.Eth) > 0 {
				conn.RUnlock()
				parser.DecodeLayers(inbound.Eth, &decoded)
				for _, t := range decoded {
				s:
					switch t {

					case layers.LayerTypeEthernet:
						destAddr := make(net.HardwareAddr, len(eth.DstMAC))
						copy(destAddr, eth.DstMAC)
						inbound.DestAddr = &destAddr
						if !bytes.Equal(eth.SrcMAC, EthernetBroadcast) {
							addr := make(net.HardwareAddr, len(eth.SrcMAC))
							copy(addr, eth.SrcMAC)
							for _, a := range conn.Addrs {
								if bytes.Equal(a, addr) {
									break s
								}
							}
							conn.Lock()
							conn.Addrs = append(conn.Addrs, addr)
							conn.Unlock()
							trigger(scope.Sub(
								&conn, &addr,
							), EvTCP, EvTCPConnGotAddr)
						}

					case layers.LayerTypeARP:
						ip := make(net.IP, len(arp.SourceProtAddress))
						copy(ip, arp.SourceProtAddress)
						for _, i := range conn.IPs {
							if i.Equal(ip) {
								break s
							}
						}
						conn.Lock()
						conn.IPs = append(conn.IPs, ip)
						conn.Unlock()
						trigger(scope.Sub(
							&conn, &ip,
						), EvTCP, EvTCPConnGotIP)

					case layers.LayerTypeIPv4:
						ip := make(net.IP, len(ipv4.SrcIP))
						copy(ip, ipv4.SrcIP)
						for _, i := range conn.IPs {
							if i.Equal(ip) {
								break s
							}
						}
						conn.Lock()
						conn.IPs = append(conn.IPs, ip)
						conn.Unlock()
						trigger(scope.Sub(
							&conn, &ip,
						), EvTCP, EvTCPConnGotIP)

					}
				}
			} else {
				conn.RUnlock()
			}

			inbound.BridgeIndex = uint8(bridgeIndex)
			select {
			case inboundCh <- inbound:
				trigger(scope.Sub(
					&conn, &inbound,
				), EvTCP, EvTCPInboundSent)
			case <-closing:
			}

		}
	}

	refreshConns := func() {
		for _, node := range network.nodes.Load().([]*Node) {
			hasTCP := false
			for _, name := range node.BridgeNames {
				if name == "TCP" {
					hasTCP = true
					break
				}
			}
			if !hasTCP {
				continue
			}

			node := node
			now := getTime()

			if node == network.LocalNode && node.WanHost != "" {
				port := getPort(node, now.Add(time.Millisecond*500))
				hostPort := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))

				// local node, listen
				if _, ok := listeners[hostPort]; ok {
					continue
				}

				// listen
				ln, err := listenConfig.Listen(context.Background(), "tcp", hostPort)
				if err != nil {
					continue
				}
				listener := &TCPListener{
					Listener:  ln,
					StartedAt: getTime(),
				}
				trigger(scope.Sub(
					&listener,
				), EvTCP, EvTCPListened)

				spawn(scope, func() {
					for {
						netConn, err := ln.Accept()
						if err != nil {
							return
						}
						trigger(scope.Sub(
							&listener, &netConn,
						), EvTCP, EvTCPAccepted)

						spawn(scope, func() {
							netConn.SetDeadline(getTime().Add(connDuration))
							conn := &TCPConn{
								TCPConn: netConn.(*net.TCPConn),
							}
							addConn(conn)
							readConn(conn)
						})

					}
				})

				listeners[hostPort] = listener

			} else if node.WanHost != "" {
				// non-local node

				port := getPort(node, now)
				hostPort := net.JoinHostPort(node.wanIP.String(), strconv.Itoa(port))
				exists := false
				for _, c := range conns {
					if c.RemoteAddr().String() == hostPort {
						exists = true
						break
					}
				}
				if exists {
					continue
				}

				// connect
				spawn(scope, func() {
					netConn, err := dialer.Dial("tcp", hostPort)
					if err != nil {
						return
					}
					trigger(scope.Sub(
						&node, &netConn,
					), EvTCP, EvTCPDialed)
					netConn.SetDeadline(getTime().Add(connDuration))
					conn := &TCPConn{
						TCPConn: netConn.(*net.TCPConn),
						IPs: []net.IP{
							node.LanIP,
						},
					}
					addConn(conn)
					readConn(conn)
				})

			}

		}

		trigger(scope, EvTCPRefreshed)
	}
	refreshConns()

	refreshConnsTicker := time.NewTicker(time.Second * 1)
	listenerCheckTicker := time.NewTicker(time.Second * 5)

	close(ready)
	trigger(scope, EvTCP, EvTCPReady)

	for {
		select {

		case outbound := <-outboundCh:
			if outbound == nil {
				break
			}

			sent := false

			for i := len(conns) - 1; i >= 0; i-- {
				conn := conns[i]

				// filter
				skip := false
				ipMatched := false
				addrMatched := false
				conn.RLock()
				if len(conn.Addrs) == 0 && len(conn.IPs) == 0 {
					skip = true
				}
				if outbound.DestIP != nil && len(conn.IPs) > 0 {
					ok := false
					for _, ip := range conn.IPs {
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
				if outbound.DestAddr != nil && len(conn.Addrs) > 0 {
					ok := false
					for _, addr := range conn.Addrs {
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
				conn.RUnlock()
				if skip {
					continue
				}

				// send
				if err := network.writeOutbound(conn, outbound); err != nil {
					conn.CloseWrite()
					trigger(scope.Sub(
						&conn, &outbound, &err,
					), EvTCP, EvTCPWriteOutboundError)
					deleteConn(conn)
					continue
				}
				sent = true

				trigger(scope.Sub(
					&conn, &outbound,
				), EvTCP, EvTCPOutboundSent)

				if ipMatched || addrMatched {
					break
				}

			}

			if !sent {
				trigger(scope.Sub(
					&outbound, &conns,
				), EvTCP, EvTCPOutboundNotSent)
			}

		case <-refreshConnsTicker.C:
			refreshConns()

		case <-listenerCheckTicker.C:
			now := getTime()
			for addr, listener := range listeners {
				if now.Sub(listener.StartedAt) > listenerDuration {
					listener.Listener.Close()
					delete(listeners, addr)
					trigger(scope.Sub(
						&listener,
					), EvTCP, EvTCPListenerClosed)
				}
			}

		case fn := <-doInLoopCh:
			fn()

		case <-closing:
			for _, ln := range listeners {
				ln.Listener.Close()
			}
			for _, conn := range conns {
				conn.SetDeadline(getTime().Add(-time.Hour))
			}
			trigger(scope, EvTCP, EvTCPClosed)
			return

		}
	}

}
