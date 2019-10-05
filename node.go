package l2

import "net"

type Node struct {
	LanIP       net.IP
	BridgeNames []string
}
