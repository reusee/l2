package l2

type Spawn func(fn func())

func (Network) Spawn(
	wg CloseWaitGroup,
) Spawn {
	return func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
}
