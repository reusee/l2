package l2

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math/rand"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/reusee/dscope"
	"github.com/reusee/e/v2"
)

var (
	pt     = fmt.Printf
	me     = e.Default.WithStack()
	ce, he = e.New(me)
)

type (
	dyn   = interface{}
	any   = interface{}
	Scope = dscope.Scope
)

func init() {
	var seed int64
	err := binary.Read(crand.Reader, binary.LittleEndian, &seed)
	ce(err)
	rand.Seed(seed)
}

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
