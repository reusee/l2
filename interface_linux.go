//go:build !tun
// +build !tun

package l2

import (
	"fmt"
	"net"

	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

func (Network) Interface(
	localNode *Node,
	ipnet net.IPNet,
	mtu MTU,
) Interface {

	interfaceType := water.DeviceType(water.TAP)
	iface, err := water.New(water.Config{
		DeviceType: interfaceType,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name:       fmt.Sprintf("L2-%s-%d", localNode.LanIP.String(), localNode.ID),
			MultiQueue: true,
		},
	})
	ce.WithInfo("new interface")(err)

	link, err := netlink.LinkByName(iface.Name())
	ce(err)
	hwAddr := ip2Mac(localNode.LanIP)
	ce(netlink.LinkSetHardwareAddr(link, hwAddr))
	err = netlink.LinkSetUp(link)
	ce(err)
	err = netlink.AddrAdd(link, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   localNode.LanIP,
			Mask: ipnet.Mask,
		},
	})
	ce(err)
	err = netlink.LinkSetMTU(link, int(mtu))
	ce(err)
	err = netlink.SetPromiscOn(link)
	ce(err)

	return iface
}
