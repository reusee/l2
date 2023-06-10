package l2

type Bridge struct {
	Start any
}

var defaultBridgeNames = []string{
	"UDP",
	"TCP",
	//"ICMP",
}
