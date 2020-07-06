// +build tun

package l2

import (
	"fmt"
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

func (n *Network) SetupInterface() {
	interfaceType := water.DeviceType(water.TUN)
	iface, err := water.New(water.Config{
		DeviceType: interfaceType,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name:       fmt.Sprintf("L2-%s-%d", n.LocalNode.LanIP.String(), n.LocalNode.ID),
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

	//TODO set MACs

	n.iface = &fakeTAP{
		iface: iface,
	}
}

type fakeTAP struct {
	iface *water.Interface
}

func (f *fakeTAP) Close() error {
	return f.iface.Close()
}

func (f *fakeTAP) Name() string {
	return f.iface.Name()
}

func (f *fakeTAP) Read(buf []byte) (n int, err error) {
	n, err = f.iface.Read(buf[14:])
	if err != nil {
		return
	}
	n += 14

	// decode ipv4 and fill MAC header
	parser := gopacket.NewDecodingLayerParser(layers.LayerTypeIPv4)
	var ipv4 layers.IPv4
	parser.AddDecodingLayer(&ipv4)
	decoded := make([]gopacket.LayerType, 0, 1)
	parser.DecodeLayers(buf[14:n], &decoded)
	dstMac := ip2Mac(ipv4.DstIP)
	copy(buf[:6], dstMac)
	srcMac := ip2Mac(ipv4.SrcIP)
	copy(buf[6:12], srcMac)
	buf[12] = 8
	buf[13] = 0

	return
}

func (f *fakeTAP) Write(buf []byte) (n int, err error) {
	parser := gopacket.NewDecodingLayerParser(layers.LayerTypeEthernet)
	var eth layers.Ethernet
	var ipv4 layers.IPv4
	var arp layers.ARP
	parser.AddDecodingLayer(&eth)
	parser.AddDecodingLayer(&ipv4)
	parser.AddDecodingLayer(&arp)
	decoded := make([]gopacket.LayerType, 0, 5)
	parser.DecodeLayers(buf, &decoded)
	for _, t := range decoded {
		switch t {
		case layers.LayerTypeEthernet:

		case layers.LayerTypeIPv4:
			n, err = f.iface.Write(buf[14:])
			if err != nil {
				return
			}
			n += 14
			return n, nil

		case layers.LayerTypeARP:

		}
	}

	return len(buf), nil
}
