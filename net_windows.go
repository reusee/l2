package l2

import (
	"net"
)

var listenConfig = &net.ListenConfig{}

var dialer = &net.Dialer{
	Timeout: dialTimeout,
}
