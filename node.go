package l2

import "net"

type Node struct {
	IP          net.IP
	BridgeNames []string
}
