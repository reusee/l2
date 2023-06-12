package l2

import "os"

type Hostname string

func (Network) Hostname() Hostname {
	hostname, err := os.Hostname()
	ce.WithInfo("get host name")(err)
	return Hostname(hostname)
}
