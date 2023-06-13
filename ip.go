package l2

import "net"

type GetReachableIP func(node *Node) net.IP

func (Network) GetReachableIP(
	sysAddrs SystemInterfaceAddrs,
) GetReachableIP {
	return func(node *Node) net.IP {
		// default to wan ip
		ip := node.wanIP
		// ip private ip provided, check if OK
		if len(ip) == 0 && len(node.PrivateIP) > 0 {
			for _, addr := range sysAddrs {
				if ipnet, ok := addr.(*net.IPNet); ok && ipnet.Contains(node.PrivateIP) {
					ip = node.PrivateIP
					break
				}
			}
		}
		return ip
	}
}
