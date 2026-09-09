// Package state holds the daemon's authoritative model of one receiver.
//
// It is the single source of truth in the system: the Mac never talks to the
// receiver, and every client reads what this package decides is true.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/musiccast"
)

// Store merges the two sources of truth about a receiver — pushed UDP events
// (partial) and polled getStatus snapshots (complete) — into one State.
type Store struct {
	mu    sync.Mutex
	state pe.State
}

// NewStore builds a Store with a fresh epoch identifying this daemon instance.
//
// Clients compare epochs to notice a restart, because Seq alone cannot tell
// "an older update from the daemon I know" apart from "the first update from a
// daemon that just started counting again".
func NewStore(r pe.Range) *Store {
	return &Store{state: pe.State{Range: r, Epoch: newEpoch()}}
}

// newEpoch returns a value unique to this process.
//
// Random rather than a timestamp: two daemons started in the same second (a
// restart loop) must not collide, and a node whose clock steps backwards
// must not reuse an epoch a client has already seen.
func newEpoch() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Falling back to the clock is worse than random but far better than
		// a constant, which would make every restart invisible to clients.
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// State returns the current snapshot.
func (m *Store) State() pe.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// ApplySnapshot folds in a complete getStatus reply, overwriting every field it
// covers. This is the reconcile path: it is how the daemon recovers from a
// dropped UDP datagram or a lapsed subscription.
func (m *Store) ApplySnapshot(s musiccast.Status) pe.State {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.state
	next.Power = pe.Power(s.Power)
	next.Volume = s.Volume
	next.Mute = s.Mute
	next.Input = s.Input
	// A snapshot only exists because the receiver answered.
	next.Online = true

	return m.commit(next)
}

// SetOnline records whether the receiver is currently reachable.
func (m *Store) SetOnline(online bool) pe.State {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.state
	next.Online = online
	return m.commit(next)
}

// SetRange records the volume scale once the device has advertised it.
//
// Separate from construction because the daemon must start even when the
// receiver is asleep, which means the scale is genuinely unknown for a while.
func (m *Store) SetRange(r pe.Range) pe.State {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.state
	next.Range = r
	return m.commit(next)
}

// ApplyPlaying records what the current source is playing.
//
// Separate from ApplySnapshot because it comes from a separate call: getStatus
// describes the zone, getPlayInfo describes the network source, and they are
// fetched independently. commit's equality check does the rest — a stream that
// pushes the same track title twice costs one comparison, not an SSE broadcast
// to every subscriber.
func (m *Store) ApplyPlaying(p pe.Playing) pe.State {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.state
	next.Playing = p
	return m.commit(next)
}

// ApplyEvent folds in a pushed UDP event. Only non-nil fields are touched: the
// device reports just what changed, so anything else in the event's absence
// must be left exactly as it was.
func (m *Store) ApplyEvent(ev musiccast.Event) pe.State {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.state
	if ev.Volume != nil {
		next.Volume = *ev.Volume
	}
	if ev.Mute != nil {
		next.Mute = *ev.Mute
	}
	if ev.Power != nil {
		next.Power = pe.Power(*ev.Power)
	}
	if ev.Input != nil {
		next.Input = *ev.Input
	}

	return m.commit(next)
}

// commit stores next and bumps Seq only if something actually changed, so a
// redundant event does not wake every SSE subscriber for nothing.
//
// Callers must hold m.mu.
func (m *Store) commit(next pe.State) pe.State {
	if next == m.state {
		return m.state
	}
	next.Seq = m.state.Seq + 1
	m.state = next
	return m.state
}
