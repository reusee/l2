package l2

import (
	"net"
	"time"

	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

func (n *Network) SetupInterface() {
	interfaceType := water.DeviceType(water.TAP)
	iface, err := water.New(water.Config{
		DeviceType: interfaceType,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name:       time.Now().Format("TAP-20060102-150405-999"),
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
			IP:   n.localNode.IP,
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
