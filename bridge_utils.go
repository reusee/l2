package l2

import (
	"fmt"
	"hash/fnv"
	"time"
)

func shiftingPorts(
	kind string,
	n int,
	portShiftInterval time.Duration,
) (
	get func(node *Node, t time.Time) []int,
) {

	type PortInfo struct {
		Time  time.Time
		Ports []int
	}
	portInfos := make(map[*Node]*PortInfo)
	get = func(node *Node, t time.Time) []int {
		t = t.Round(portShiftInterval)
		info, ok := portInfos[node]
		if !ok {
			info = new(PortInfo)
			portInfos[node] = info
		}
		if t != info.Time {
			// shift
			for i := 0; i < n; i++ {
				f := fnv.New64a()
				if i > 0 {
					if node.ID > 0 {
						fmt.Fprintf(f, "%d-%s-%s-%d-%d",
							i, kind, node.lanIPStr, node.ID, t.Unix(),
						)
					} else {
						fmt.Fprintf(f, "%d-%s-%s-%d",
							i, kind, node.lanIPStr, t.Unix(),
						)
					}
				} else {
					if node.ID > 0 {
						fmt.Fprintf(f, "%s-%s-%d-%d",
							kind, node.lanIPStr, node.ID, t.Unix(),
						)
					} else {
						fmt.Fprintf(f, "%s-%s-%d",
							kind, node.lanIPStr, t.Unix(),
						)
					}
				}
				port := 10000 + f.Sum64()%45000
				info.Time = t
				info.Ports = append(info.Ports, int(port))
			}
		}
		return info.Ports
	}

	return
}
