package l2

import (
	"errors"
	"fmt"

	"github.com/reusee/dscope"
	"github.com/reusee/e5"
)

var (
	pt = fmt.Printf
	we = e5.Wrap.With(e5.WrapStacktrace)
	ce = e5.Check.With(e5.WrapStacktrace)
	he = e5.Handle
	is = errors.Is
)

type (
	Scope = dscope.Scope
)
