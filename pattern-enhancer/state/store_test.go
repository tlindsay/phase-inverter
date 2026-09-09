package state

import (
	"testing"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/musiccast"
)

func intPtr(i int) *int { return &i }

func strPtr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func newTestStore() *Store {
	m := NewStore(pe.Range{Min: 0, Max: 80, Step: 1})
	m.ApplySnapshot(musiccast.Status{
		Power: "on", Volume: 45, Mute: false, Input: "phono",
	})
	return m
}

func TestApplyEventMergesOnlyChangedFields(t *testing.T) {
	m := newTestStore()

	got := m.ApplyEvent(musiccast.Event{Volume: intPtr(46)})

	if got.Volume != 46 {
		t.Errorf("Volume = %d, want 46", got.Volume)
	}
	// The volume-only event must not disturb anything else. This is the bug
	// that a naive struct-overwrite merge introduces.
	if got.Input != "phono" {
		t.Errorf("Input = %q, want %q (unchanged)", got.Input, "phono")
	}
	if got.Power != pe.PowerOn {
		t.Errorf("Power = %q, want %q (unchanged)", got.Power, pe.PowerOn)
	}
	if got.Mute {
		t.Error("Mute = true, want false (unchanged)")
	}
}

func TestSeqIncrementsOnEveryRealChange(t *testing.T) {
	m := newTestStore()

	first := m.ApplyEvent(musiccast.Event{Volume: intPtr(46)})
	second := m.ApplyEvent(musiccast.Event{Volume: intPtr(47)})

	if second.Seq <= first.Seq {
		t.Errorf("Seq did not increase: first=%d second=%d", first.Seq, second.Seq)
	}
}

func TestSeqUnchangedWhenEventChangesNothing(t *testing.T) {
	m := newTestStore()

	before := m.ApplyEvent(musiccast.Event{Volume: intPtr(46)})
	after := m.ApplyEvent(musiccast.Event{Volume: intPtr(46)})

	// A no-op event must not bump seq, or every redundant datagram would
	// wake every SSE subscriber for nothing.
	if after.Seq != before.Seq {
		t.Errorf("Seq bumped on a no-op event: before=%d after=%d", before.Seq, after.Seq)
	}
}

func TestApplyEventUpdatesInputAndPower(t *testing.T) {
	m := newTestStore()

	got := m.ApplyEvent(musiccast.Event{
		Input: strPtr("spotify"),
		Power: strPtr("standby"),
		Mute:  boolPtr(true),
	})

	if got.Input != "spotify" {
		t.Errorf("Input = %q, want %q", got.Input, "spotify")
	}
	if got.Power != pe.PowerStandby {
		t.Errorf("Power = %q, want %q", got.Power, pe.PowerStandby)
	}
	if !got.Mute {
		t.Error("Mute = false, want true")
	}
}

func TestApplyPlayingBumpsSeqOnlyWhenTheTrackChanges(t *testing.T) {
	m := newTestStore()
	playing := pe.Playing{Artist: "National", Track: "Mistaken For Strangers"}

	first := m.ApplyPlaying(playing)
	if first.Playing != playing {
		t.Fatalf("Playing = %+v, want %+v", first.Playing, playing)
	}

	// The receiver re-reports the same track constantly while a station plays.
	// Treating each as a change would wake every SSE subscriber for nothing.
	again := m.ApplyPlaying(playing)
	if again.Seq != first.Seq {
		t.Errorf("Seq = %d for an unchanged track, want %d", again.Seq, first.Seq)
	}

	next := m.ApplyPlaying(pe.Playing{Artist: "National", Track: "Bloodbuzz Ohio"})
	if next.Seq != first.Seq+1 {
		t.Errorf("Seq = %d after a new track, want %d", next.Seq, first.Seq+1)
	}
}
