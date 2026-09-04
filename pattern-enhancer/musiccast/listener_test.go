package musiccast

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestListenerDeliversParsedEvents(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := l.Events(ctx)

	sendDatagram(t, l.Port(), realVolumeEvent)

	select {
	case ev := <-events:
		if ev.Volume == nil || *ev.Volume != 46 {
			t.Errorf("Volume = %v, want 46", ev.Volume)
		}
		if ev.DeviceID != "00A0DE000000" {
			t.Errorf("DeviceID = %q, want %q", ev.DeviceID, "00A0DE000000")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
}

func TestListenerPortIsDiscoverableWhenEphemeral(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer l.Close()

	// The port has to be readable back: it is what gets advertised to the
	// receiver in X-AppPort, so binding :0 is only usable if the daemon can
	// find out what it actually got.
	if l.Port() == 0 {
		t.Error("Port() = 0 after binding an ephemeral port")
	}
}

func TestListenerSkipsMalformedDatagrams(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := l.Events(ctx)

	// A garbage datagram from anything else on the network must not kill the
	// listener, or one stray packet ends event push until the next restart.
	sendDatagram(t, l.Port(), "this is not json")
	sendDatagram(t, l.Port(), realVolumeEvent)

	select {
	case ev := <-events:
		if ev.Volume == nil || *ev.Volume != 46 {
			t.Errorf("Volume = %v, want 46 — listener did not survive garbage", ev.Volume)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out; listener probably died on the malformed datagram")
	}
}

func TestListenerStopsWhenContextCancelled(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	events := l.Events(ctx)
	cancel()

	select {
	case _, open := <-events:
		if open {
			t.Error("channel delivered a value after cancellation; want it closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event channel was not closed after context cancellation")
	}
}

func sendDatagram(t *testing.T, port int, payload string) {
	t.Helper()
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dialing listener: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("writing datagram: %v", err)
	}
}
