package l2

func startICMP(
	ready Ready,
	scope Scope,
	spawn Spawn,
	closing Closing,
	outboundCh chan *Outbound,
) {

	close(ready)

	for {
		select {

		case <-closing:
			return

		case outbound := <-outboundCh:
			outbound.Eth.Put()

		}
	}

}
