package l2

type SelectNode func() *Node

func (Network) SelectNode() SelectNode {
	return nil
}
