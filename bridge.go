package l2

type Bridge struct {
	Start dyn
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

var allBridgeNames = func() []string {
	var names []string
	for name := range availableBridges {
		names = append(names, name)
	}
	return names
}()
