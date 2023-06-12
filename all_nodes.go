package l2

type AllNodes []*Node

func (Network) AllNodes(
	localNode *Node,
	initNodes InitNodes,
) AllNodes {
	ret := make([]*Node, len(initNodes))
	copy(ret, initNodes)

	// add local node to network
	var existed bool
	for _, node := range ret {
		if node.LanIP.Equal(localNode.LanIP) {
			existed = true
			break
		}
	}
	if !existed {
		ret = append(ret, localNode)
	}

	for _, node := range ret {
		node.Init()
	}

	return ret
}
