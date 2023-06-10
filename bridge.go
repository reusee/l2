package l2

type Bridge struct {
	Start any
}

var availableBridges = map[string]Bridge{
	"TCP": {
		Start: startTCP,
	},
	"UDP": {
		Start: startUDP,
	},
	"ICMP": {
		Start: startICMP,
	},
}

var defaultBridgeNames = []string{
	"UDP",
	"TCP",
	//"ICMP",
}
