package l2

import (
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishvananda/netns"
)

func testPingPong(
	t *testing.T,
	getNetwork1 func() *Network,
	getNetwork2 func() *Network,
) {
	defer func() {
		var err error
		he(&err)
		if err != nil {
			t.Fatal(err)
		}
	}()

	// node1
	var ln net.Listener
	ok1 := make(chan struct{})
	var network1 *Network
	go func() {
		runtime.LockOSThread()
		ns0, err := netns.Get()
		ce(err)
		_ = ns0
		ns1, err := netns.New()
		ce(err)
		_ = ns1

		network1 = getNetwork1()
		err = network1.Start()
		ce(err)
		if !network1.Network.Contains(network1.LocalNode.LanIP) {
			t.Fatal()
		}

		ln, err = net.Listen("tcp", network1.LocalNode.LanIP.String()+":34567")
		ce(err)
		network1.Scope.Call(func(
			spawn Spawn,
			scope Scope,
		) {
			network1.Scope.Call(func(
				on On,
			) {
				on(EvNetworkClosing, func() {
					ln.Close()
				})
			})
			spawn(scope, func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					spawn(scope, func() {
						defer conn.Close()
						for {
							var s string
							ce(json.NewDecoder(conn).Decode(&s))
							ce(json.NewEncoder(conn).Encode(s))
							if s == "quit" {
								return
							}
						}
					})
				}
			})
		})

		close(ok1)
	}()
	<-ok1

	network2 := getNetwork2()
	err := network2.Start()
	ce(err)
	if !network2.Network.Contains(network2.LocalNode.LanIP) {
		t.Fatal()
	}

	retry := 10
connect:
	conn, err := net.Dial("tcp", network1.LocalNode.LanIP.String()+":34567")
	if err != nil && retry > 0 {
		time.Sleep(time.Millisecond * 200)
		retry--
		goto connect
	}
	ce(err)
	var s string
	for i := 0; i < 1024; i++ {
		input := strings.Repeat("foobar", i)
		ce(json.NewEncoder(conn).Encode(input))
		ce(json.NewDecoder(conn).Decode(&s))
		if s != input {
			t.Fatal()
		}
	}
	ce(json.NewEncoder(conn).Encode("quit"))
	ce(json.NewDecoder(conn).Decode(&s))
	conn.Close()

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go func() {
		defer wg.Done()
		network1.Close()
	}()
	go func() {
		defer wg.Done()
		network2.Close()
	}()
	wg.Wait()

}
