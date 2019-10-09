// +build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/reusee/e/v2"
	"github.com/reusee/l2"
)

var (
	pt     = fmt.Printf
	me     = e.Default
	ce, he = e.New(me)
)

func main() {
	nodeSH := &l2.Node{
		LanIP:       net.IPv4(192, 168, 42, 1),
		WanHost:     "sh.reus.mobi",
		BridgeNames: []string{"UDP"},
	}
	nodeHK := &l2.Node{
		LanIP:       net.IPv4(192, 168, 42, 2),
		WanHost:     "hk.reus.mobi",
		BridgeNames: []string{"UDP"},
	}
	nodeUS := &l2.Node{
		LanIP:       net.IPv4(192, 168, 42, 3),
		WanHost:     "us.reus.mobi",
		BridgeNames: []string{"UDP"},
	}

	network := &l2.Network{
		Network: net.IPNet{
			IP:   net.IPv4(192, 168, 42, 0),
			Mask: net.CIDRMask(24, 32),
		},
		InitNodes: []*l2.Node{
			nodeSH, nodeHK, nodeUS,
		},
		MTU:       1345,
		CryptoKey: []byte("abc4567890123456"),

		SelectNode: func(
			addrs []net.Addr,
			hostname l2.Hostname,
		) *l2.Node {
			if hostname == "qcloud-reus-arch" {
				return nodeSH
			} else if hostname == "iZj6cfn7x0vybyjnw9pvcsZ" {
				return nodeHK
			}
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok {
					if ipnet.IP.Equal(net.IPv4(67, 216, 200, 158)) {
						return nodeUS
					}
				}
			}
			return nil
		},
	}

	if err := network.Start(); err != nil {
		panic(err)
	}
	pt("%+v\n", network.LocalNode)
	defer network.Close()

	go startSocksServer(network.LocalNode.LanIP.String() + ":1080")

	select {}

}

type SocksConn struct {
	ClientPort int
	HostPort   string
}

func startSocksServer(addr string) {
	ln, err := net.Listen("tcp", addr)
	ce(err, "listen")
	for {
		c, err := ln.Accept()
		ce(err, "accept")
		go func() {
			conn := c.(*net.TCPConn)
			hostPort, err := Socks5Handshake(conn)
			if err != nil {
				conn.Close()
				return
			}
			proxyHost(conn, hostPort)
		}()
	}
}

const (
	VERSION = byte(5)

	METHOD_NOT_REQUIRED  = byte(0)
	METHOD_NO_ACCEPTABLE = byte(0xff)

	RESERVED = byte(0)

	ADDR_TYPE_IP     = byte(1)
	ADDR_TYPE_IPV6   = byte(4)
	ADDR_TYPE_DOMAIN = byte(3)

	CMD_CONNECT       = byte(1)
	CMD_BIND          = byte(2)
	CMD_UDP_ASSOCIATE = byte(3)

	REP_SUCCEED                    = byte(0)
	REP_SERVER_FAILURE             = byte(1)
	REP_CONNECTION_NOT_ALLOW       = byte(2)
	REP_NETWORK_UNREACHABLE        = byte(3)
	REP_HOST_UNREACHABLE           = byte(4)
	REP_CONNECTION_REFUSED         = byte(5)
	REP_TTL_EXPIRED                = byte(6)
	REP_COMMAND_NOT_SUPPORTED      = byte(7)
	REP_ADDRESS_TYPE_NOT_SUPPORTED = byte(8)
)

func Socks5Handshake(conn net.Conn) (hostPort string, err error) {
	defer he(&err, "socks5 server handshake")

	conn.SetReadDeadline(time.Now().Add(time.Second * 8))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 8))

	var greetings struct {
		Version        byte
		NumAuthMethods byte
	}
	ce(binary.Read(conn, binary.BigEndian, &greetings))
	if greetings.Version != VERSION {
		err = me(nil, "bad version")
		return
	}
	authMethods := make([]byte, int(greetings.NumAuthMethods))
	_, err = io.ReadFull(conn, authMethods)
	ce(err)
	if bytes.IndexByte(authMethods, METHOD_NOT_REQUIRED) == -1 {
		err = me(nil, "bad auth method")
		return
	}
	_, err = conn.Write([]byte{
		VERSION,
		METHOD_NOT_REQUIRED,
	})
	ce(err)

	var request struct {
		Version     byte
		Command     byte
		_           byte
		AddressType byte
	}
	ce(binary.Read(conn, binary.BigEndian, &request))
	if request.Version != VERSION {
		err = me(nil, "bad version")
		return
	}
	if request.Command != CMD_CONNECT {
		err = me(nil, "bad command")
		return
	}
	if request.AddressType != ADDR_TYPE_IP &&
		request.AddressType != ADDR_TYPE_DOMAIN &&
		request.AddressType != ADDR_TYPE_IPV6 {
		err = me(nil, "bad address type")
		return
	}
	var host string
	switch request.AddressType {
	case ADDR_TYPE_IP:
		bs := make([]byte, 4)
		_, err = io.ReadFull(conn, bs)
		ce(err)
		host = net.IP(bs).String()
	case ADDR_TYPE_IPV6:
		bs := make([]byte, 16)
		_, err = io.ReadFull(conn, bs)
		ce(err)
		host = net.IP(bs).String()
	case ADDR_TYPE_DOMAIN:
		var l uint8
		ce(binary.Read(conn, binary.BigEndian, &l))
		bs := make([]byte, int(l))
		_, err = io.ReadFull(conn, bs)
		ce(err)
		host = string(bs)
	}
	var port uint16
	ce(binary.Read(conn, binary.BigEndian, &port))

	_, err = conn.Write([]byte{
		VERSION,
		REP_SUCCEED,
		RESERVED,
		ADDR_TYPE_IP,
		0, 0, 0, 0,
		0, 0,
	})
	ce(err)

	hostPort = net.JoinHostPort(host, strconv.Itoa(int(port)))

	return
}

func proxyHost(conn *net.TCPConn, hostPort string) {
	retry := 5
dial:
	tConn, err := net.Dial("tcp", hostPort)
	if err != nil {
		if retry > 0 {
			retry--
			time.Sleep(time.Millisecond * 100)
			goto dial
		}
		conn.Close()
		return
	}
	targetConn := tConn.(*net.TCPConn)

	proxyConn(conn, targetConn)
}

type ProxyConnCtx struct {
	OnError func(err error, bytesRead int64)
}

func proxyConn(conn, targetConn *net.TCPConn) {
	proxyConnCtx(conn, targetConn, ProxyConnCtx{})
}

func proxyConnCtx(conn, targetConn *net.TCPConn, ctx ProxyConnCtx) {
	var bytesRead int64

	go func() {
		r := proxyConnReader{
			r: conn,
			before: func() {
				conn.SetReadDeadline(time.Now().Add(time.Minute * 64))
				targetConn.SetWriteDeadline(time.Now().Add(time.Minute * 64))
			},
			after: func(n int) {
				atomic.AddInt64(&bytesRead, int64(n))
			},
		}
		_, err := io.Copy(targetConn, r)
		if err != nil {
			if ctx.OnError != nil {
				ctx.OnError(err, atomic.LoadInt64(&bytesRead))
			}
		}
		conn.CloseRead()
		targetConn.CloseWrite()
	}()

	r := proxyConnReader{
		r: targetConn,
		before: func() {
			targetConn.SetReadDeadline(time.Now().Add(time.Minute * 64))
			conn.SetWriteDeadline(time.Now().Add(time.Minute * 64))
		},
	}
	_, err := io.Copy(conn, r)
	if err != nil {
		if ctx.OnError != nil {
			ctx.OnError(err, atomic.LoadInt64(&bytesRead))
		}
	}
	targetConn.CloseRead()
	conn.CloseWrite()

}

type proxyConnReader struct {
	before func()
	r      io.Reader
	after  func(int)
}

var _ io.Reader = proxyConnReader{}

func (r proxyConnReader) Read(bs []byte) (int, error) {
	if r.before != nil {
		r.before()
	}
	n, err := r.r.Read(bs)
	if r.after != nil {
		r.after(n)
	}
	return n, err
}
