package l2

import (
	"net"
	"time"
)

var listenConfig = &net.ListenConfig{}

var dialer = &net.Dialer{
	Timeout: time.Second * 16,
}
