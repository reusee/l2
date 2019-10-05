package l2

import (
	"net"

	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

func (n *Network) SetupInterface() {
	interfaceType := water.DeviceType(water.TAP)
	iface, err := water.New(water.Config{
		DeviceType: interfaceType,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name:       "L-" + n.localNode.LanIP.String(),
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
			IP:   n.localNode.LanIP,
			Mask: n.Network.Mask,
		},
	})
	ce(err)
	err = netlink.LinkSetMTU(link, n.MTU)
	ce(err)
	err = netlink.SetPromiscOn(link)
	ce(err)

	n.iface = iface
}
