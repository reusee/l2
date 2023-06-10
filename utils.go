package l2

import (
	"hash/fnv"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func dumpEth(bs []byte) {
	packet := gopacket.NewPacket(bs, layers.LayerTypeEthernet, gopacket.Default)
	pt("-----------------\n")
	for _, layer := range packet.Layers() {
		pt("%v %T %+v\n", layer.LayerType(), layer, layer)
	}
	pt(".................\n")
}

func hash64(bs []byte) uint64 {
	h := fnv.New64()
	_, err := h.Write(bs)
	ce(err)
	return h.Sum64()
}
