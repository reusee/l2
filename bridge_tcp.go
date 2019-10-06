package l2

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"time"
)

func startTCP(
	scope Scope,
	network *Network,
	closing Closing,
	spawn Spawn,
	outboundCh chan Outbound,
	inboundCh chan Inbound,
) {

	nodes := make(map[string]*Node)
	for _, node := range nodes {
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
		nodes[node.lanIPStr] = node
	}

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
			port := 10000 + f.Sum64()%55000
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
	conns := make(map[*Node][]*net.TCPConn)

	refreshConns := func() {
		for _, node := range nodes {
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
				ln, err := listenConfig.Listen(context.Background(), "tcp", fmt.Sprintf("0.0.0.0:%d", port))
				if err != nil {
					continue
				}

				spawn(scope, func() {
					for {
						conn, err := ln.Accept()
						if err != nil {
							return
						}
						conn.SetDeadline(getTime().Add(connDuration))
						//TODO
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
					//TODO
				})

			}

		}
	}
	refreshConns()

	refreshConnsTicker := time.NewTicker(time.Second * 1)
	listenerCheckTicker := time.NewTicker(time.Second * 1)

	for {
		select {

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

		case <-closing:
			for _, ln := range listeners {
				ln.Listener.Close()
			}

		}
	}

}
