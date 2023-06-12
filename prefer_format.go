package l2

import (
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

type PreferFormatFunc func(
	*layers.Ethernet,
	*layers.ARP,
	*layers.IPv4,
	*layers.TCP,
	*layers.UDP,
	*[]gopacket.LayerType,
) WireFormat

func (Network) PreferFormatFunc() PreferFormatFunc {
	return nil
}
