package musiccast

import (
	"context"
	"net"
)

// maxDatagram bounds a single read. Observed events are ~50 bytes; this leaves
// generous room for the larger netusb/play_info notifications without letting a
// hostile sender dictate an allocation.
const maxDatagram = 8192

// Listener receives the receiver's pushed UDP notifications.
//
// The device sends to whatever source address made an HTTP request bearing the
// X-AppName/X-AppPort headers, and keeps doing so for ten minutes. It is a
// best-effort channel: datagrams can be lost, so nothing may depend on having
// seen every one. The reconcile poll exists to cover exactly that.
type Listener struct {
	conn *net.UDPConn
}

// Listen binds a UDP socket. Port 0 selects an ephemeral port, which Port then
// reports back for advertising in X-AppPort.
func Listen(port int) (*Listener, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		return nil, err
	}
	return &Listener{conn: conn}, nil
}

// Port reports the bound port.
func (l *Listener) Port() int {
	return l.conn.LocalAddr().(*net.UDPAddr).Port
}

func (l *Listener) Close() error {
	return l.conn.Close()
}

// Events streams parsed notifications until ctx is cancelled, at which point
// the channel is closed.
//
// Malformed datagrams are dropped rather than surfaced: this socket is
// reachable by anything on the LAN, and a single stray packet must not be able
// to end event delivery.
func (l *Listener) Events(ctx context.Context) <-chan Event {
	out := make(chan Event)

	go func() {
		defer close(out)

		// Unblocks the read below on cancellation. Closing the socket is the
		// only way to interrupt ReadFromUDP, which does not take a context.
		go func() {
			<-ctx.Done()
			_ = l.conn.Close()
		}()

		buf := make([]byte, maxDatagram)
		for {
			n, _, err := l.conn.ReadFromUDP(buf)
			if err != nil {
				return // socket closed, or unrecoverable
			}

			ev, err := ParseEvent(buf[:n])
			if err != nil {
				continue
			}

			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}
