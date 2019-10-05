package l2

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand"

	"github.com/reusee/e/v2"
)

var (
	pt     = fmt.Printf
	me     = e.Default.WithStack()
	ce, he = e.New(me)
)

type (
	dyn = interface{}
	any = interface{}
)

func init() {
	var seed int64
	binary.Read(crand.Reader, binary.LittleEndian, &seed)
	rand.Seed(seed)
}
