package l2

func startICMP(
	ready Ready,
	scope Scope,
	spawn Spawn,
) {

	spawn(scope, func(
		closing Closing,
		outboundCh chan Outbound,
	) {
		for {
			select {

			case <-closing:
				return

			case outbound := <-outboundCh:
				outbound.Eth.Put()

			}
		}
	})

	close(ready)
	//TODO
}
