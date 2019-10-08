package l2

import (
	"time"

	"github.com/beevik/ntp"
)

var getTime = func() func() time.Time {
	servers := []string{
		"time.cloudflare.com",
		"cn.ntp.org.cn",
	}
	ret := make(chan time.Time, 1)
	for _, server := range servers {
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
		sysTime0 := time.Now()
		return func() time.Time {
			return ntpTime0.Add(time.Since(sysTime0))
		}
	case <-time.After(time.Second * 3):
		panic("get ntp time timeout")
	}
}()
