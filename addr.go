package l2

import "net"

type SystemInterfaceAddrs []net.Addr

func (Network) SystemInterfaceAddrs() SystemInterfaceAddrs {
	var ifaceAddrs []net.Addr
	ifaces, err := net.Interfaces()
	ce(err)
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		ce(err)
		ifaceAddrs = append(ifaceAddrs, addrs...)
	}
	return ifaceAddrs
}
