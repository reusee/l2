package l2

import (
	"bytes"
	"encoding/binary"
	"net"
	"time"
)

type sendQueue struct {
	network       *Network
	initCountDown int
	timerDuration time.Duration
	timer         *time.Timer
	timerStarted  bool
	m             map[queueKey]*queueValue
	sendFunc      queueSendFunc
}

type queueKey struct {
	IPLen   int
	IP      [16]byte
	HasAddr bool
	Addr    [6]byte
}

type queueValue struct {
	countDown int
	length    int
	datas     [][]byte
}

type queueSendFunc func(*net.IP, *net.HardwareAddr, []byte)

func newSendQueue(
	network *Network,
	sendFunc queueSendFunc,
) *sendQueue {
	return &sendQueue{
		network:       network,
		initCountDown: 0,
		timerDuration: time.Microsecond * 1500,
		timer:         time.NewTimer(time.Microsecond * 1500),
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
	var ipPtr *net.IP
	if key.IPLen > 0 {
		ip := net.IP(key.IP[:key.IPLen])
		ipPtr = &ip
	}
	var addrPtr *net.HardwareAddr
	if key.HasAddr {
		addr := net.HardwareAddr(key.Addr[:])
		addrPtr = &addr
	}
	q.sendFunc(ipPtr, addrPtr, buf.Bytes())
}

func (q *sendQueue) enqueue(
	outbound *Outbound,
) {
	var key queueKey
	if outbound.DestIP != nil {
		key.IPLen = len(*outbound.DestIP)
		copy(key.IP[:], *outbound.DestIP)
	}
	if outbound.DestAddr != nil {
		key.HasAddr = true
		copy(key.Addr[:], *outbound.DestAddr)
	}

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

	if q.initCountDown <= 0 {
		q.send(key)
		return
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
		if value.countDown <= 0 {
			q.send(key)
		}
	}
	if len(q.m) > 0 {
		q.timer.Reset(q.timerDuration)
		q.timerStarted = true
	} else {
		q.timerStarted = false
	}
}
