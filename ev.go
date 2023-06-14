package l2

import "sync"

const (
	EvTCP = iota + 1
	EvTCPReady
	EvTCPConnAdded
	EvTCPConnDeleted
	EvTCPReadInboundError
	EvTCPConnGotAddr
	EvTCPConnGotIP
	EvTCPInboundSent
	EvTCPListening
	EvTCPListenFailed
	EvTCPListened
	EvTCPAccepted
	EvTCPDialing
	EvTCPDialed
	EvTCPDialFailed
	EvTCPRefreshed
	EvTCPWriteError
	EvTCPNotSent
	EvTCPListenerClosed
	EvTCPClosed
	EvTCPSlow

	EvUDP
	EvUDPReady
	EvUDPRemoteAdded
	EvUDPLocalAdded
	EvUDPConnReadError
	EvUDPReadInboundError
	EvUDPLocalClosed
	EvUDPRemoteClosed
	EvUDPRemoteGotAddr
	EvUDPRemoteGotIP
	EvUDPInboundSent
	EvUDPWriteError
	EvUDPNotSent
	EvUDPClosed

	EvICMP
	EvICMPReady
	EvICMPClosed
	EvICMPNotSent
	EvICMPInboundSent
	EvICMPReadInboundError
	EvICMPWriteError

	EvNetwork
	EvNetworkClosing
	EvNetworkOutboundSent
	EvNetworkInboundReceived
	EvNetworkInboundDuplicated
	EvNetworkInboundWritten
)

type (
	On      func(ev any, fn any)
	Trigger func(scope Scope, evs ...any)
)

func (Network) Events() (
	on On,
	trigger Trigger,
) {

	type callback struct {
		fn      any
		oneshot bool
	}
	callbacks := make(map[any][]callback)
	var l sync.Mutex

	on = func(ev any, fn any) {
		l.Lock()
		defer l.Unlock()
		callbacks[ev] = append(callbacks[ev], callback{
			fn: fn,
		})
	}

	trigger = func(scope Scope, evs ...any) {
		for _, ev := range evs {
			l.Lock()
			cs := callbacks[ev]
			if len(cs) == 0 {
				l.Unlock()
				continue
			}
			var fns []any
			i := 0
			for i < len(cs) {
				callback := cs[i]
				fns = append(fns, callback.fn)
				if callback.oneshot {
					cs = append(cs[:i], cs[i+1:]...)
					continue
				}
				i++
			}
			callbacks[ev] = cs
			l.Unlock()
			for _, fn := range fns {
				scope.Call(fn)
			}
		}
	}

	return
}
