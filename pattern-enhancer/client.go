package patternenhancer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client drives a pattern-enhancer daemon over its REST + SSE contract.
//
// Commands and the event stream are independent on purpose: a command is an
// ordinary stateless request, so it still works while the stream is down. On a
// laptop that sleeps and changes networks, that is the difference between the
// first keypress after waking working and blocking on a reconnect.
type Client struct {
	baseURL  string
	deviceID string
	http     *http.Client
}

func NewClient(baseURL, deviceID string) *Client {
	return &Client{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		deviceID: deviceID,
		// Commands must fail fast: a hotkey that hangs for 30 seconds is
		// worse than one that reports an error and lets the user retry.
		http: &http.Client{Timeout: 4 * time.Second},
	}
}

func (c *Client) devicePath(suffix string) string {
	return fmt.Sprintf("%s/v1/devices/%s/%s", c.baseURL, c.deviceID, suffix)
}

// State fetches the daemon's authoritative snapshot.
func (c *Client) State(ctx context.Context) (State, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.devicePath("state"), nil)
	if err != nil {
		return State{}, err
	}
	return c.doState(req)
}

// AdjustVolume moves the volume by steps and returns the confirmed result.
//
// Steps are device units, sized against the Range carried in State — never a
// percentage. Callers batching encoder detents should send one accumulated
// count rather than one request per detent.
func (c *Client) AdjustVolume(ctx context.Context, steps int) (State, error) {
	return c.post(ctx, "volume", map[string]any{"step": steps})
}

// SetInput switches inputs.
func (c *Client) SetInput(ctx context.Context, input string) (State, error) {
	return c.post(ctx, "input", map[string]any{"input": input})
}

// TogglePower flips power, letting the receiver invert its own state.
func (c *Client) TogglePower(ctx context.Context) (State, error) {
	return c.post(ctx, "power", map[string]any{"toggle": true})
}

// SetPower drives power to an explicit state.
func (c *Client) SetPower(ctx context.Context, on bool) (State, error) {
	return c.post(ctx, "power", map[string]any{"on": on})
}

// ToggleMute flips mute.
func (c *Client) ToggleMute(ctx context.Context) (State, error) {
	return c.post(ctx, "mute", map[string]any{"toggle": true})
}

func (c *Client) post(ctx context.Context, suffix string, body map[string]any) (State, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return State{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.devicePath(suffix), bytes.NewReader(payload))
	if err != nil {
		return State{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doState(req)
}

func (c *Client) doState(req *http.Request) (State, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return State{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return State{}, fmt.Errorf("pattern-enhancer: %s %s returned HTTP %d",
			req.Method, req.URL.Path, resp.StatusCode)
	}

	var st State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return State{}, fmt.Errorf("pattern-enhancer: decoding state: %w", err)
	}
	return st, nil
}

// reconnectDelay is how long to wait before re-establishing a dropped event
// stream. Short enough that a lid-open feels instant, long enough that a daemon
// which is genuinely down is not hammered.
const reconnectDelay = 2 * time.Second

// Events streams state until ctx is cancelled, reconnecting on its own.
//
// The daemon sends a full snapshot on connect, so a reconnect needs no replay
// or cursor: whatever arrives first after reconnecting is simply the truth.
// Dropped connections are therefore normal operation rather than an error path.
func (c *Client) Events(ctx context.Context) <-chan State {
	out := make(chan State)

	go func() {
		defer close(out)
		for {
			c.streamOnce(ctx, out)

			if ctx.Err() != nil {
				return
			}
			select {
			case <-time.After(reconnectDelay):
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// streamOnce holds one SSE connection until it fails or ctx ends.
func (c *Client) streamOnce(ctx context.Context, out chan<- State) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.devicePath("events"), nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	// No timeout on the stream: it is meant to stay open indefinitely, so the
	// command client's deadline would kill it on a schedule.
	streamer := &http.Client{}
	resp, err := streamer.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		var st State
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if err := json.Unmarshal([]byte(payload), &st); err != nil {
			continue
		}

		select {
		case out <- st:
		case <-ctx.Done():
			return
		}
	}
}
