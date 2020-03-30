package l2

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
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
	closeOnce sync.Once
	IPs       []net.IP
	Addrs     []net.HardwareAddr
	T0        time.Time
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
	ifaceAddr net.HardwareAddr,
	localAddrs []net.Addr,
) {

	// port
	portShiftInterval := time.Millisecond * 5113
	listenerDuration := portShiftInterval * 2
	connDuration := portShiftInterval * 3
	getPorts := shiftingPorts(
		1,
		fmt.Sprintf("%x-tcp-", network.CryptoKey),
		portShiftInterval,
	)

	// listener
	listeners := make(map[string]*TCPListener)

	// conn
	conns := make(map[string]*TCPConn)

	// sync callback
	doInLoopCh := make(chan func(), 1024)
	doInLoop := func(fn func()) {
		select {
		case doInLoopCh <- fn:
		case <-closing:
		}
	}

	// conn funcs
	deleteConn := func(conn *TCPConn) {
		doInLoop(func() {
			// delete conn from conns
			for hostPort, c := range conns {
				if c == conn {
					delete(conns, hostPort)
					trigger(scope.Sub(
						&c,
					), EvTCP, EvTCPConnDeleted)
				}
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
			var length uint16
			if err := binary.Read(conn, binary.LittleEndian, &length); err != nil {
				break
			}
			inbound, err := network.readInbound(
				&io.LimitedReader{
					R: conn,
					N: int64(length),
				},
			)

			if err != nil {
				trigger(scope.Sub(
					&conn, &err,
				), EvTCP, EvTCPReadInboundError)
				conn.closeOnce.Do(func() {
					conn.Close()
				})
				select {
				case <-closing:
				default:
					deleteConn(conn)
				}
				return
			}

			conn.RLock()
			if (len(conn.Addrs) == 0 || len(conn.IPs) == 0) && len(inbound.Eth) > 0 {
				conn.RUnlock()
				parser.DecodeLayers(inbound.Eth, &decoded)
				for _, t := range decoded {
				s:
					switch t {

					case layers.LayerTypeEthernet:
						if !bytes.Equal(eth.DstMAC, EthernetBroadcast) {
							destAddr := make(net.HardwareAddr, len(eth.DstMAC))
							copy(destAddr, eth.DstMAC)
							inbound.DestAddr = &destAddr
						}
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
						if !IPv4zero.Equal(arp.SourceProtAddress) {
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
						}

					case layers.LayerTypeIPv4:
						if !IPv4zero.Equal(ipv4.SrcIP) {
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

			if node == network.LocalNode {
				ports := getPorts(node, now.Add(time.Millisecond*500))
				for _, port := range ports {
					port := port
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
								if err := netConn.SetDeadline(getTime().Add(connDuration)); err != nil {
									return
								}
								conn := &TCPConn{
									TCPConn: netConn.(*net.TCPConn),
									T0:      time.Now(),
								}
								doInLoop(func() {
									conns[hostPort] = conn
									trigger(scope.Sub(
										&conn,
									), EvTCP, EvTCPConnAdded)
								})
								readConn(conn)
							})

						}
					})

					listeners[hostPort] = listener
				}

			} else {
				// non-local node

				ip := node.wanIP
				if len(ip) == 0 && len(node.PrivateIP) > 0 {
					for _, addr := range localAddrs {
						if ipnet, ok := addr.(*net.IPNet); ok && ipnet.Contains(node.PrivateIP) {
							ip = node.PrivateIP
							break
						}
					}
				}

				if len(ip) > 0 {
					ports := getPorts(node, now)
					for _, port := range ports {
						port := port
						hostPort := net.JoinHostPort(ip.String(), strconv.Itoa(port))
						if _, ok := conns[hostPort]; ok {
							continue
						}
						doInLoop(func() {
							conns[hostPort] = nil
						})

						// connect
						spawn(scope, func() {
							netConn, err := dialer.Dial("tcp", hostPort)
							if err != nil {
								return
							}
							trigger(scope.Sub(
								&node, &netConn,
							), EvTCP, EvTCPDialed)
							if err := netConn.SetDeadline(getTime().Add(connDuration)); err != nil {
								return
							}
							conn := &TCPConn{
								TCPConn: netConn.(*net.TCPConn),
								IPs: []net.IP{
									node.LanIP,
								},
								T0: time.Now(),
							}
							trigger(scope.Sub(
								&conn, &node.LanIP,
							), EvTCP, EvTCPConnGotIP)
							doInLoop(func() {
								conns[hostPort] = conn
								trigger(scope.Sub(
									&conn,
								), EvTCP, EvTCPConnAdded)
							})
							readConn(conn)
						})
					}
				}

			}

		}

		trigger(scope, EvTCPRefreshed)
	}
	refreshConns()

	refreshConnsTicker := time.NewTicker(time.Second * 1)
	listenerCheckTicker := time.NewTicker(time.Second * 5)

	close(ready)
	trigger(scope, EvTCP, EvTCPReady)

	type qKey struct {
		IPLen   int
		IP      [16]byte
		HasAddr bool
		Addr    [6]byte
	}
	queue := newSendQueue(
		network,
		func(ip *net.IP, addr *net.HardwareAddr, data []byte) {
			sent := false

			var candidates []*TCPConn
			for _, conn := range conns {
				if conn == nil {
					continue
				}
				skip := false
				conn.RLock()
				if len(conn.Addrs) == 0 && len(conn.IPs) == 0 {
					skip = true
				}
				if time.Since(conn.T0) > portShiftInterval*2 {
					skip = true
				}
				if ip != nil && len(conn.IPs) > 0 {
					ok := false
					for _, connIP := range conn.IPs {
						if connIP.Equal(*ip) {
							ok = true
							break
						}
					}
					if !ok {
						skip = true
					}
				}
				if addr != nil && len(conn.Addrs) > 0 {
					ok := false
					for _, connAddr := range conn.Addrs {
						if bytes.Equal(connAddr, *addr) {
							ok = true
							break
						}
					}
					if !ok {
						skip = true
					}
				}
				conn.RUnlock()
				if skip {
					continue
				}
				candidates = append(candidates, conn)
			}

			// send
			for _, i := range rand.Perm(len(candidates)) {
				conn := candidates[i]
				if _, err := conn.Write(data); err != nil {
					conn.closeOnce.Do(func() {
						conn.Close()
					})
					trigger(scope.Sub(
						&conn, &err,
					), EvTCP, EvTCPWriteError)
					deleteConn(conn)
					continue
				}
				sent = true
				break
			}

			if !sent {
				trigger(scope.Sub(
					&conns,
				), EvTCP, EvTCPNotSent)
			}
		},
	)

	for {
		select {

		case outbound := <-outboundCh:
			if outbound == nil {
				break
			}
			queue.enqueue(outbound)

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

		case <-queue.timer.C:
			queue.tick()

		case fn := <-doInLoopCh:
			fn()

		case <-closing:
			for _, ln := range listeners {
				ln.Listener.Close()
			}
			for _, conn := range conns {
				if conn != nil {
					_ = conn.SetDeadline(getTime().Add(-time.Hour))
				}
			}
			trigger(scope, EvTCP, EvTCPClosed)
			return

		}
	}

}
