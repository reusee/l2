package l2

import (
	"sync"
	"time"
)

var bytesClasses, bytesPools = func() (
	classes []int,
	pools []sync.Pool,
) {
	for i := 7; i <= 16; i++ {
		size := 1 << i
		classes = append(classes, size)
		pools = append(pools, sync.Pool{
			New: func() interface{} {
				return make([]byte, size)
			},
		})
	}
	return
}()

type Bytes struct {
	*Life
	Bytes []byte
	class int
}

func getBytes(size int) Bytes {
	class := 0
	for size > bytesClasses[class] {
		class++
		if class == len(bytesClasses) {
			break
		}
	}
	life := new(Life)
	life.SetMax(time.Second * 1)
	if class == len(bytesClasses) {
		return Bytes{
			Life:  life,
			Bytes: make([]byte, size),
			class: -1,
		}
	}
	return Bytes{
		Life:  life,
		Bytes: bytesPools[class].Get().([]byte)[:size],
		class: class,
	}
}

func (b Bytes) Put() {
	if b.class >= 0 {
		bytesPools[b.class].Put(b.Bytes)
	}
	b.End()
}
