package l2

import (
	"fmt"
	"net"
	"runtime"

	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

func (n *Network) SetupInterfaces() {
	for i := 0; i < runtime.NumCPU(); i++ {
		interfaceType := water.DeviceType(water.TAP)
		iface, err := water.New(water.Config{
			DeviceType: interfaceType,
			PlatformSpecificParams: water.PlatformSpecificParams{
				Name:       fmt.Sprintf("L%d-", i) + n.LocalNode.LanIP.String(),
				MultiQueue: true,
			},
		})
		ce(err, "new interface")

		link, err := netlink.LinkByName(iface.Name())
		ce(err)
		err = netlink.LinkSetUp(link)
		ce(err)
		err = netlink.AddrAdd(link, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   n.LocalNode.LanIP,
				Mask: n.Network.Mask,
			},
		})
		ce(err)
		err = netlink.LinkSetMTU(link, n.MTU)
		ce(err)
		err = netlink.SetPromiscOn(link)
		ce(err)

		n.ifaces = append(n.ifaces, iface)
	}
}
