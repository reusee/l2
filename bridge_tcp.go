package l2

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"sync"
	"time"
)

func startTCP(
	ready Ready,
	scope Scope,
	network *Network,
	closing Closing,
	spawn Spawn,
	outboundCh chan Outbound,
	inboundCh chan Inbound,
	inboundSenderGroup *sync.WaitGroup,
) {

	portShiftInterval := time.Second * 11
	listenerDuration := portShiftInterval + time.Second*59
	connDuration := portShiftInterval + time.Second*10

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

	type Listener struct {
		Listener  net.Listener
		StartedAt time.Time
	}
	listeners := make(map[string]*Listener)
	conns := make(map[*Node][]net.Conn)

	doInLoopCh := make(chan func(), 1024)
	doInLoop := func(fn func()) {
		select {
		case doInLoopCh <- fn:
		case <-closing:
		}
	}

	readConn := func(conn net.Conn) {
		inboundSenderGroup.Add(1)
		defer inboundSenderGroup.Done()
		for {
			inbound, err := network.readInbound(conn)
			if err != nil {
				conn.Close()
				select {
				case <-closing:
				default:
					doInLoop(func() {
						for node, cs := range conns {
							for i := 0; i < len(cs); {
								if cs[i] == conn {
									cs = append(cs[:i], cs[i+1:]...)
								}
							}
							conns[node] = cs
						}
					})
				}
				return
			}
			select {
			case inboundCh <- inbound:
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
			port := getPort(node, now)
			tcpAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", node.wanIP, port))
			ce(err, "resolve tcp addr")
			tcpAddrStr := tcpAddr.String()

			if node == network.localNode {
				// local node, listen
				if _, ok := listeners[tcpAddrStr]; ok {
					continue
				}

				// listen
				hostPort := net.JoinHostPort(node.WanHost, strconv.Itoa(port))
				ln, err := listenConfig.Listen(context.Background(), "tcp", hostPort)
				if err != nil {
					continue
				}

				spawn(scope, func() {
					for {
						conn, err := ln.Accept()
						if err != nil {
							return
						}

						spawn(scope, func() {
							conn.SetDeadline(getTime().Add(connDuration))
							//TODO add to conns
							readConn(conn)
						})

					}
				})

				listeners[tcpAddrStr] = &Listener{
					Listener:  ln,
					StartedAt: getTime(),
				}

			} else {
				// non-local node
				exists := false
				for _, c := range conns[node] {
					if c.RemoteAddr().String() == tcpAddrStr {
						exists = true
						break
					}
				}
				if exists {
					continue
				}

				// connect
				spawn(scope, func() {
					conn, err := dialer.Dial("tcp", tcpAddrStr)
					if err != nil {
						return
					}
					conn.SetDeadline(getTime().Add(connDuration))
					doInLoop(func() {
						conns[node] = append(conns[node], conn)
					})
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
			func() {
				defer outbound.Eth.Put()

				sent := false

				if outbound.IsBroadcast {
					// broadcast
				loop_node:
					for node, cs := range conns {
						if node == network.localNode {
							continue
						}
						for _, conn := range cs {
							if err := network.writeOutbound(conn, outbound); err != nil {
								continue
							} else {
								sent = true
								continue loop_node
							}
						}
					}

				} else if outbound.DestNode != nil {
					// node
					for _, conn := range conns[outbound.DestNode] {
						if err := network.writeOutbound(conn, outbound); err != nil {
							continue
						} else {
							sent = true
							break
						}
					}

				} else {
					dumpEth(outbound.Eth.Bytes)
					panic("not handled")
				}

				if !sent {
					//pt("%s %d not sent\n", network.localNode.lanIPStr, hash64(outbound.Eth.Bytes))
					//dumpEth(outbound.Eth.Bytes)
					//pt("%+v\n", outbound)
					//pt("%+v\n", outbound.DestNode)
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
			for _, cs := range conns {
				for _, c := range cs {
					c.SetDeadline(getTime().Add(-time.Hour))
				}
			}
			return

		}
	}

}
