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
	outboundCh chan *Outbound,
	inboundCh chan *Inbound,
	inboundSenderGroup *sync.WaitGroup,
) {

	portShiftInterval := time.Second * 7
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

	deleteConn := func(conn net.Conn) {
		doInLoop(func() {
			// delete conn from conns
			for node, cs := range conns {
				for i := 0; i < len(cs); {
					if cs[i] == conn {
						cs = append(cs[:i], cs[i+1:]...)
						continue
					}
					i++
				}
				conns[node] = cs
			}
		})
	}

	readConn := func(conn net.Conn, unknownNode bool) {
		inboundSenderGroup.Add(1)
		defer inboundSenderGroup.Done()
		nodeRecognized := make(chan struct{})
		var once sync.Once
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

			if unknownNode {
				select {
				case <-nodeRecognized:
				default:
					inbound.UnknownNode = func(node *Node) {
						once.Do(func() {
							doInLoop(func() {
								conns[node] = append(conns[node], conn)
							})
							close(nodeRecognized)
						})
					}
				}
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

			if node == network.localNode && node.WanHost != "" {
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
						conn, err := ln.Accept()
						if err != nil {
							return
						}

						spawn(scope, func() {
							conn.SetDeadline(getTime().Add(connDuration))
							readConn(conn, true)
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
				for _, c := range conns[node] {
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
					conn, err := dialer.Dial("tcp", hostPort)
					if err != nil {
						return
					}
					conn.SetDeadline(getTime().Add(connDuration))
					doInLoop(func() {
						conns[node] = append(conns[node], conn)
					})
					readConn(conn, false)
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

				if outbound.IsBroadcast {
					// broadcast
				loop_node:
					for node, cs := range conns {
						if node == network.localNode {
							continue
						}
						for i := len(cs) - 1; i >= 0; i-- {
							conn := cs[i]
							if err := network.writeOutbound(conn, outbound); err != nil {
								deleteConn(conn)
								continue
							} else {
								sent = true
								continue loop_node
							}
						}
					}

				} else if outbound.DestNode != nil {
					// node
					cs := conns[outbound.DestNode]
					for i := len(cs) - 1; i >= 0; i-- {
						conn := cs[i]
						if err := network.writeOutbound(conn, outbound); err != nil {
							deleteConn(conn)
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
			for _, cs := range conns {
				for _, c := range cs {
					c.SetDeadline(getTime().Add(-time.Hour))
				}
			}
			return

		}
	}

}
