package l2

import "sync"

type Close func()

type Closing chan struct{}

type CloseWaitGroup struct {
	*sync.WaitGroup
}

func (n Network) Close(
	trigger Trigger,
) (
	closing Closing,
	wg CloseWaitGroup,
	_close Close,
) {

	closing = make(Closing)

	wg.WaitGroup = new(sync.WaitGroup)

	var closeOnce sync.Once
	_close = func() {
		closeOnce.Do(func() {
			close(closing)
			trigger(n.RootScope, EvNetworkClosing)
			wg.Wait()
		})
	}

	return
}
