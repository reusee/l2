package l2

import (
	"encoding/binary"
	"io"
)

type Outbound struct {
	Eth         Bytes
	IsBroadcast bool
	DestNode    *Node
	Serial      uint64
}

type Inbound struct {
	Eth         Bytes
	OnNodeFound func(*Node)
	Serial      uint64
}

func (n *Network) writeOutbound(w io.Writer, outbound *Outbound) error {
	if err := binary.Write(w, binary.LittleEndian, uint16(len(outbound.Eth.Bytes))); err != nil {
		return err
	}
	_, err := w.Write(outbound.Eth.Bytes)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, outbound.Serial); err != nil {
		return err
	}
	return nil
}

func (n *Network) readInbound(r io.Reader) (inbound Inbound, err error) {
	var l uint16
	if err = binary.Read(r, binary.LittleEndian, &l); err != nil {
		return
	}
	bs := getBytes(int(l))
	if _, err = io.ReadFull(r, bs.Bytes); err != nil {
		bs.Put()
		return
	}
	inbound.Eth = bs
	if err = binary.Read(r, binary.LittleEndian, &inbound.Serial); err != nil {
		return
	}
	return
}
