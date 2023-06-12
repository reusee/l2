package l2

import "net"

type SystemUnicastAddrs []net.Addr

func (Network) SystemUnicastAddrs() SystemUnicastAddrs {
	addrs, err := net.InterfaceAddrs()
	ce.WithInfo("get interface addrs")(err)
	return addrs
}

type SystemAllAddrs []net.Addr

func (Network) SystemAllAddrs() SystemAllAddrs {
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
