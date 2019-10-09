package l2

import (
	"fmt"
	"hash/fnv"
	"time"
)

func shiftingPort(
	kind string,
	portShiftInterval time.Duration,
) (
	get func(node *Node, t time.Time) int,
) {

	type PortInfo struct {
		Time time.Time
		Port int
	}
	portInfos := make(map[*Node]*PortInfo)
	get = func(node *Node, t time.Time) int {
		t = t.Round(portShiftInterval)
		info, ok := portInfos[node]
		if !ok {
			info = new(PortInfo)
			portInfos[node] = info
		}
		if t != info.Time {
			// shift
			f := fnv.New64a()
			fmt.Fprintf(f, "%s-%s-%d",
				kind, node.lanIPStr, t.Unix(),
			)
			port := 10000 + f.Sum64()%45000
			info.Time = t
			info.Port = int(port)
		}
		return info.Port
	}

	return
}
