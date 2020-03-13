package l2

/*

#include <sys/socket.h>
#include <net/if.h>
#include <net/ndrv.h>
#include <net/bpf.h>
#include <fcntl.h>
#include <unistd.h>

*/
import "C"
import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type macInterface struct {
	if0Name string
	if1Name string
	bpfFD   uintptr
}

func (n *Network) SetupInterface() {
	iface := new(macInterface)

	if0Name := fmt.Sprintf("feth%d", rand.Intn(100)+1)
	iface.if0Name = if0Name
	out, err := exec.Command(
		"ifconfig",
		if0Name,
		"create",
	).CombinedOutput()
	ce(err, out)

	if1Name := fmt.Sprintf("feth%d", rand.Intn(100)+200)
	iface.if1Name = if1Name
	out, err = exec.Command(
		"ifconfig",
		if1Name,
		"create",
	).CombinedOutput()
	ce(err, out)

	time.Sleep(time.Millisecond * 200)

	out, err = exec.Command(
		"ifconfig",
		if0Name,
		"peer",
		if1Name,
	).CombinedOutput()
	ce(err, out)

	out, err = exec.Command(
		"ifconfig",
		if0Name,
		"mtu", strconv.Itoa(n.MTU),
		"up",
	).CombinedOutput()
	ce(err, out)

	out, err = exec.Command(
		"ifconfig",
		if0Name,
		"inet",
		n.LocalNode.LanIP.String(),
	).CombinedOutput()
	ce(err, out)

	out, err = exec.Command(
		"ifconfig",
		if1Name,
		"mtu", strconv.Itoa(n.MTU),
		"up",
	).CombinedOutput()
	ce(err, out)

	f, err := os.OpenFile("/dev/bpf2", os.O_RDWR, 0755)
	ce(err)
	bpfFD := f.Fd()
	iface.bpfFD = bpfFD

	n.iface = iface
}

func (m macInterface) Close() (err error) {
	defer he(&err)

	C.close(C.int(m.bpfFD))

	out, err := exec.Command(
		"ifconfig",
		m.if0Name,
		"destroy",
	).CombinedOutput()
	ce(err, out)

	out, err = exec.Command(
		"ifconfig",
		m.if1Name,
		"destroy",
	).CombinedOutput()
	ce(err, out)

	return nil
}

func (m macInterface) Name() string {
	return m.if0Name
}

func (m macInterface) Read(buf []byte) (int, error) {
	//TODO
	return 0, nil
}

func (m macInterface) Write(data []byte) (int, error) {
	//TODO
	return 0, nil
}
