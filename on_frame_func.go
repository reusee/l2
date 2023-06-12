package l2

type OnFrameFunc func([]byte)

func (Network) OnFrameFunc() OnFrameFunc {
	return nil
}
