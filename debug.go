package l2

import (
	"net/http"
	_ "net/http/pprof"
)

func init() {
	//TODO
	go func() {
		ce(http.ListenAndServe(":23456", nil))
	}()
}
