package l2

import "container/heap"

type InboundQueue []*Inbound

var _ heap.Interface = new(InboundQueue)

func (q InboundQueue) Len() int {
	return len(q)
}

func (q InboundQueue) Less(i, j int) bool {
	return q[i].Serial < q[j].Serial
}

func (q InboundQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

func (q *InboundQueue) Push(x any) {
	*q = append(*q, x.(*Inbound))
}

func (q *InboundQueue) Pop() any {
	old := *q
	l := len(old)
	x := old[l-1]
	*q = old[:l-1]
	return x
}
