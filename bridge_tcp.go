package l2

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func startTCP(
	ready Ready,
	scope Scope,
	network *Network,
	closing Closing,
	spawn Spawn,
	outboundCh chan *Outbound,
	inboundCh chan *Inbound,
	inboundSenderGroup *sync.WaitGroup,
) {

	// port
	portShiftInterval := time.Second * 11
	listenerDuration := portShiftInterval * 2
	connDuration := portShiftInterval * 16
	type PortInfo struct {
		Time time.Time
		Port int
	}
	portInfos := make(map[*Node]*PortInfo)
	getPort := func(node *Node, t time.Time) int {
		t = t.Round(portShiftInterval)
		info, ok := portInfos[node]
		if !ok {
			info = new(PortInfo)
			portInfos[node] = info
		}
		if t != info.Time {
			// shift
			f := fnv.New64a()
			fmt.Fprintf(f, "tcp-%s-%s-%d",
				network.CryptoKey, node.lanIPStr, t.Unix(),
			)
			port := 10000 + f.Sum64()%45000
			info.Time = t
			info.Port = int(port)
		}
		return info.Port
	}

	// listener
	type Listener struct {
		Listener  net.Listener
		StartedAt time.Time
	}
	listeners := make(map[string]*Listener)

	// conn
	type Conn struct {
		sync.RWMutex
		net.Conn
		IPs   []net.IP
		Addrs []net.HardwareAddr
	}
	var conns []*Conn

	// sync callback
	doInLoopCh := make(chan func(), 1024)
	doInLoop := func(fn func()) {
		select {
		case doInLoopCh <- fn:
		case <-closing:
		}
	}

	// conn funcs
	addConn := func(conn *Conn) {
		doInLoop(func() {
			conns = append(conns, conn)
		})
	}
	deleteConn := func(conn *Conn) {
		doInLoop(func() {
			// delete conn from conns
			for i := 0; i < len(conns); {
				if conns[i] == conn {
					conns = append(conns[:i], conns[i+1:]...)
					continue
				}
				i++
			}
		})
	}

	readConn := func(conn *Conn) {
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
				conn.Close()
				select {
				case <-closing:
				default:
					deleteConn(conn)
				}
				return
			}

			conn.RLock()
			if len(conn.Addrs) == 0 || len(conn.IPs) == 0 {
				conn.RUnlock()
				parser.DecodeLayers(inbound.Eth.Bytes, &decoded)
				for _, t := range decoded {
					switch t {

					case layers.LayerTypeEthernet:
						if !bytes.Equal(eth.SrcMAC, EthernetBroadcast) {
							addr := make(net.HardwareAddr, len(eth.SrcMAC))
							copy(addr, eth.SrcMAC)
							conn.Lock()
							conn.Addrs = append(conn.Addrs, addr)
							conn.Unlock()
						}

					case layers.LayerTypeARP:
						ip := make(net.IP, len(arp.SourceProtAddress))
						copy(ip, arp.SourceProtAddress)
						conn.Lock()
						conn.IPs = append(conn.IPs, ip)
						conn.Unlock()

					case layers.LayerTypeIPv4:
						ip := make(net.IP, len(ipv4.SrcIP))
						copy(ip, ipv4.SrcIP)
						conn.Lock()
						conn.IPs = append(conn.IPs, ip)
						conn.Unlock()

					}
				}
			} else {
				conn.RUnlock()
			}

			select {
			case inboundCh <- &inbound:
			case <-closing:
				inbound.Eth.Put()
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

				spawn(scope, func() {
					for {
						netConn, err := ln.Accept()
						if err != nil {
							return
						}

						spawn(scope, func() {
							netConn.SetDeadline(getTime().Add(connDuration))
							conn := &Conn{
								Conn: netConn,
							}
							addConn(conn)
							readConn(conn)
						})

					}
				})

				listeners[hostPort] = &Listener{
					Listener:  ln,
					StartedAt: getTime(),
				}

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
					netConn.SetDeadline(getTime().Add(connDuration))
					conn := &Conn{
						Conn: netConn,
						IPs: []net.IP{
							node.LanIP,
						},
					}
					addConn(conn)
					readConn(conn)
				})

			}

		}
	}
	refreshConns()

	refreshConnsTicker := time.NewTicker(time.Second * 1)
	listenerCheckTicker := time.NewTicker(time.Second * 5)

	close(ready)

	for {
		select {

		case outbound := <-outboundCh:
			if outbound == nil {
				break
			}
			func() {
				defer outbound.Eth.Put()

				sent := false

				if outbound.DestAddr == nil {
					// broadcast, send one frame per ip
					sentIPs := make(map[string]bool)
				loop_conn:
					for i := len(conns) - 1; i >= 0; i-- {
						conn := conns[i]
						// ip
						conn.RLock()
						if len(conn.IPs) > 0 {
							for _, ip := range conn.IPs {
								if sentIPs[ip.String()] {
									continue loop_conn
								}
							}
						}
						conn.RUnlock()
						if err := network.writeOutbound(conn, outbound); err != nil {
							deleteConn(conn)
							continue
						}
						sent = true
						conn.RLock()
						for _, ip := range conn.IPs {
							sentIPs[ip.String()] = true
						}
						conn.RUnlock()
					}

				} else {
				loop_conn2:
					for i := len(conns) - 1; i >= 0; i-- {
						conn := conns[i]
						conn.RLock()
						// ip
						if outbound.DestIP != nil && len(conn.IPs) > 0 {
							ok := false
							for _, ip := range conn.IPs {
								if ip.Equal(*outbound.DestIP) {
									ok = true
								}
							}
							if !ok {
								continue loop_conn2
							}
						}
						// addr
						if outbound.DestAddr != nil && len(conn.Addrs) > 0 {
							ok := false
							for _, addr := range conn.Addrs {
								if bytes.Equal(addr, *outbound.DestAddr) {
									ok = true
								}
							}
							if !ok {
								continue loop_conn2
							}
						}
						conn.RUnlock()
						if err := network.writeOutbound(conn, outbound); err != nil {
							deleteConn(conn)
							continue
						}
						sent = true
					}

				}

				if !sent {
					//pt("--- not sent ---\n")
					//pt("serial %d\n", outbound.Serial)
					//dumpEth(outbound.Eth.Bytes)
					//pt("conns %v\n", conns[outbound.DestNode])
				}

			}()

		case <-refreshConnsTicker.C:
			refreshConns()

		case <-listenerCheckTicker.C:
			now := getTime()
			for addr, listener := range listeners {
				if now.Sub(listener.StartedAt) > listenerDuration {
					listener.Listener.Close()
					delete(listeners, addr)
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
			return

		}
	}

}
