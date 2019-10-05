package l2

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os/exec"
	"time"

	"github.com/songgao/water"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func (n *Network) SetupInterface() {
	interfaceType := water.DeviceType(water.TAP)
	iface, err := water.New(water.Config{
		DeviceType: interfaceType,
		PlatformSpecificParams: water.PlatformSpecificParams{
			ComponentID: "tap0901",
		},
	})
	ce(err, "new interface")

	out, err := exec.Command(
		"netsh",
		"int",
		"ip",
		"set",
		"address",
		iface.Name(),
		"static",
		n.localNode.LanIP.String(),
		"255.255.255.0",
		"none",
		"1",
	).CombinedOutput()
	ce(err, string(fromGBK(out)))

	out, err = exec.Command(
		"netsh",
		"int",
		"ip",
		"set",
		"dns",
		iface.Name(),
		"static",
		"127.0.0.1",
	).CombinedOutput()
	ce(err, string(fromGBK(out)))

	out, err = exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
		iface.Name(),
		fmt.Sprintf(`mtu=%d`, n.MTU),
		"store=persistent",
	).CombinedOutput()
	ce(err, string(fromGBK(out)))

	time.Sleep(time.Second * 3)

	n.iface = iface
}

func fromGBK(bs []byte) []byte {
	r := transform.NewReader(bytes.NewReader(bs), simplifiedchinese.GBK.NewDecoder())
	out, err := ioutil.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return out
}
