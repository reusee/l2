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

var (
	testCryptoKey = []byte("12345678901234567890123456789012")
	testMTU       = MTU(300)
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
		var start1 Start
		network1.RootScope.Assign(&start1)
		ce(start1())
		var net1 net.IPNet
		network1.RootScope.Assign(&net1)
		var node1 *Node
		network1.RootScope.Assign(&node1)
		if !net1.Contains(node1.LanIP) {
			panic("fail")
		}

		ln, err := net.Listen("tcp", node1.LanIP.String()+":34567")
		ce(err)
		network1.RootScope.Call(func(
			spawn Spawn,
		) {
			network1.RootScope.Call(func(
				on On,
			) {
				on(EvNetworkClosing, func() {
					ln.Close()
				})
			})
			spawn(func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					spawn(func() {
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
	var start2 Start
	network2.RootScope.Assign(&start2)
	ce(start2())
	var net2 net.IPNet
	network2.RootScope.Assign(&net2)
	var node2 *Node
	network2.RootScope.Assign(&node2)
	if !net2.Contains(node2.LanIP) {
		t.Fatal()
	}
	var node1 *Node
	network1.RootScope.Assign(&node1)

	retry := 100
connect:
	conn, err := net.Dial("tcp", node1.LanIP.String()+":34567")
	if err != nil && retry > 0 {
		time.Sleep(time.Millisecond * 200)
		retry--
		goto connect
	}
	ce(err)
	var s string
	for i := 0; i < 128; i++ {
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
		var close1 Close
		network1.RootScope.Assign(&close1)
		close1()
	}()
	go func() {
		defer wg.Done()
		var close2 Close
		network1.RootScope.Assign(&close2)
		close2()
	}()
	wg.Wait()

}
