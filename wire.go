package l2

type Outbound struct {
	Eth         Bytes
	IsBroadcast bool
	DestNode    *Node
}

type Inbound struct {
	Eth Bytes
}
