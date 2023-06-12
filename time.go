package l2

import (
	"time"

	"github.com/beevik/ntp"
)

type GetTime func() time.Time

var ntpServers = []string{
	"time.cloudflare.com",
	"162.159.200.1",
	"162.159.200.123",

	"cn.ntp.org.cn",
	"58.220.133.132",
	"114.118.7.163",
	"223.113.120.194",
	"114.118.7.161",
	"223.113.97.104",
	"120.25.115.20",
	"120.25.108.11",
	"203.107.6.88",
	"182.92.12.11",
	"119.177.128.146",
	"118.24.195.65",

	"ntp6a.rollernet.us",
	"time.google.com",
}

func (Network) GetTime() GetTime {
	ret := make(chan time.Time, 1)
	for _, server := range ntpServers {
		server := server
		go func() {
			t0, err := ntp.Time(server)
			if err != nil {
				return
			}
			select {
			case ret <- t0:
			default:
			}
		}()
	}
	select {
	case ntpTime0 := <-ret:
		ntpTime0 = ntpTime0.UTC()
		sysTime0 := time.Now().UTC()
		return func() time.Time {
			return ntpTime0.Add(time.Since(sysTime0))
		}
	case <-time.After(time.Second * 3):
		panic("get ntp time timeout")
	}
}
