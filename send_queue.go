package l2

import (
	"bytes"
	"encoding/binary"
	"time"
)

type queueSendFunc func(queueKey, *queueValue, []byte)

type sendQueue struct {
	network       *Network
	initCountDown int
	timerDuration time.Duration
	timer         *time.Timer
	timerStarted  bool
	m             map[queueKey]*queueValue
	sendFunc      queueSendFunc
}

type queueKey any

type queueValue struct {
	countDown int
	length    int
	datas     [][]byte
}

func newSendQueue(
	network *Network,
	sendFunc queueSendFunc,
) *sendQueue {
	return &sendQueue{
		network:       network,
		initCountDown: 1,
		timerDuration: time.Millisecond * 5,
		timer:         time.NewTimer(time.Millisecond * 5),
		timerStarted:  true,
		m:             make(map[queueKey]*queueValue),
		sendFunc:      sendFunc,
	}
}

func (q *sendQueue) send(
	key queueKey,
) {
	value := q.m[key]
	delete(q.m, key)
	buf := new(bytes.Buffer)
	for _, data := range value.datas {
		ce(binary.Write(buf, binary.LittleEndian, uint16(len(data))))
		_, err := buf.Write(data)
		ce(err)
	}
	q.sendFunc(key, value, buf.Bytes())
}

func (q *sendQueue) enqueue(
	key queueKey,
	outbound *Outbound,
) {
	buf := new(bytes.Buffer)
	if err := q.network.writeOutbound(buf, outbound); err != nil {
		panic(err)
	}
	data := buf.Bytes()
	inQueue, ok := q.m[key]
	if ok {
		if inQueue.length+len(data)+2 > int(q.network.MTU) {
			q.send(key)
			q.m[key] = &queueValue{
				countDown: q.initCountDown,
				length:    len(data) + 2, // include uint16 length
				datas:     [][]byte{data},
			}
		} else {
			inQueue.length += len(data) + 2
			inQueue.datas = append(inQueue.datas, data)
		}
	} else {
		q.m[key] = &queueValue{
			countDown: q.initCountDown,
			length:    len(data) + 2,
			datas:     [][]byte{data},
		}
	}

	if len(q.m) > 0 && !q.timerStarted {
		if !q.timer.Stop() {
			select {
			case <-q.timer.C:
			default:
			}
		}
		q.timer.Reset(q.timerDuration)
		q.timerStarted = true
	}

}

func (q *sendQueue) tick() {
	for key, value := range q.m {
		value.countDown--
		if value.countDown == 0 {
			q.send(key)
		}
	}
	q.timerStarted = false
}
