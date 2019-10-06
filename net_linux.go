package l2

import (
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var listenConfig = &net.ListenConfig{
	Control: func(network, address string, conn syscall.RawConn) error {
		ce(conn.Control(func(fd uintptr) {
			ce(syscall.SetsockoptInt(int(fd), syscall.SOL_TCP, unix.TCP_FASTOPEN, 1))
			ce(syscall.SetsockoptInt(int(fd), syscall.SOL_TCP, unix.TCP_NODELAY, 1))
			ce(syscall.SetsockoptInt(int(fd), syscall.SOL_TCP, unix.TCP_QUICKACK, 1))
		}))
		return nil
	},
}

var dialer = &net.Dialer{
	Timeout: time.Second * 16,
	Control: func(network, address string, conn syscall.RawConn) error {
		ce(conn.Control(func(fd uintptr) {
			ce(syscall.SetsockoptInt(int(fd), syscall.SOL_TCP, unix.TCP_FASTOPEN_CONNECT, 1))
			ce(syscall.SetsockoptInt(int(fd), syscall.SOL_TCP, unix.TCP_NODELAY, 1))
			ce(syscall.SetsockoptInt(int(fd), syscall.SOL_TCP, unix.TCP_QUICKACK, 1))
		}))
		return nil
	},
}
