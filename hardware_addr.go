package l2

import "net"

type HardwareAddr = net.HardwareAddr

func (Network) HardwareAddr(
	iface Interface,
) HardwareAddr {
	if name := iface.Name(); name != "" {
		netInterface, err := net.InterfaceByName(name)
		ce(err)
		return netInterface.HardwareAddr
	}
	return nil
}
