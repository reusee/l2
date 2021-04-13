package l2

import (
	"testing"

	"github.com/reusee/dscope"
	"github.com/reusee/e4qa"
	"github.com/reusee/pa"
	"github.com/reusee/qa"
)

func TestQA(t *testing.T) {

	// qa
	defs := dscope.Methods(new(qa.Def))
	// e4qa
	defs = append(defs, dscope.Methods(new(e4qa.Def))...)
	// pa
	defs = append(defs, qa.AnalyzersToDefs(pa.Analyzers)...)

	dscope.New(defs...).Sub(func() qa.Args {
		return []string{"./..."}
	}).Call(func(
		check qa.CheckFunc,
	) {
		errs := check()
		if len(errs) > 0 {
			for _, err := range errs {
				pt("-> %s\n", err.Error())
			}
			t.Fatal()
		}
	})

}
