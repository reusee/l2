package l2

import (
	"net/http"
	_ "net/http/pprof"
)

func init() {
	return
	go func() {
		ce(http.ListenAndServe(":8888", nil))
	}()
}
