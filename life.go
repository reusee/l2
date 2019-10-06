package l2

import (
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type Life struct {
	ended int32

	sync.Mutex
	endOnce  sync.Once
	endFuncs []func()
	moved    bool
}

func (l *Life) OnEnd(fn func()) {
	l.Lock()
	l.endFuncs = append(l.endFuncs, fn)
	l.Unlock()
}

func (l *Life) End() {
	l.endOnce.Do(func() {
		for _, fn := range l.endFuncs {
			fn()
		}
		atomic.AddInt32(&l.ended, 1)
	})
}

func (l *Life) Move(l2 *Life) {
	l.Lock()
	if l.moved {
		panic("already moved")
	}
	l2.OnEnd(l.End)
	l.moved = true
	l.Unlock()
}

func (l *Life) SetMax(d time.Duration) {
	stack := debug.Stack()
	time.AfterFunc(d, func() {
		if atomic.LoadInt32(&l.ended) == 0 {
			pt("%s\n", stack)
			panic("live too long")
		}
	})
}
