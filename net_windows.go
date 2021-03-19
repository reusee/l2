package l2

import (
	"net"
	"time"
)

var listenConfig = &net.ListenConfig{}

func newDialer() *net.Dialer {
	return &net.Dialer{
		Timeout: time.Second * 16,
	}
}

var dialer = newDialer()
