package musiccast

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// appName identifies this daemon to the receiver. It is half of the UDP event
// subscription: the device pairs it with X-AppPort to decide where to push.
const appName = "MusicCast/1.0 (pattern-enhancer)"

// Client speaks Yamaha Extended Control to one receiver over HTTP.
//
// The receiver answers in ~20ms for both reads and writes, measured against the
// office R-N303, so the timeout here is generous rather than tuned.
type Client struct {
	baseURL string
	appPort int
	http    *http.Client
}

func NewClient(host string, appPort int) *Client {
	return &Client{
		baseURL: "http://" + host + "/YamahaExtendedControl/v1",
		appPort: appPort,
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

// apiError reports a request the device understood and refused. response_code 0
// means success; anything else is a documented failure code.
type apiError struct {
	path string
	code int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("musiccast: %s returned response_code %d", e.path, e.code)
}

// get issues one YXC request and decodes the reply into out, which may be nil
// when only the response code matters.
//
// Every request carries the subscription headers. The device's UDP push
// subscription lasts ten minutes and is renewed by any request bearing them, so
// piggybacking on ordinary traffic keeps it alive without a dedicated keepalive
// for as long as the daemon is polling at all.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-AppName", appName)
	req.Header.Set("X-AppPort", strconv.Itoa(c.appPort))

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("musiccast: %s returned HTTP %d", path, resp.StatusCode)
	}

	// The body is read whole rather than streamed because it must be decoded
	// twice: once to check response_code, once into the caller's type. These
	// replies are a few hundred bytes.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var envelope struct {
		ResponseCode int `json:"response_code"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("musiccast: decoding %s: %w", path, err)
	}
	if envelope.ResponseCode != 0 {
		return &apiError{path: path, code: envelope.ResponseCode}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// GetStatus fetches a complete snapshot of the main zone.
func (c *Client) GetStatus(ctx context.Context) (Status, error) {
	var s Status
	err := c.get(ctx, "/main/getStatus", nil, &s)
	return s, err
}

// SetVolumeRelative moves the volume by steps, signed for direction.
//
// This is deliberately relative rather than absolute: the receiver does the
// arithmetic against its own authoritative value, so a stale cache here cannot
// produce a wrong result. That is the failure the previous MQTT stack had —
// it computed absolute targets from a cache that went stale the moment anyone
// touched the physical knob.
func (c *Client) SetVolumeRelative(ctx context.Context, steps int) error {
	if steps == 0 {
		return nil
	}

	direction := "up"
	magnitude := steps
	if steps < 0 {
		direction = "down"
		magnitude = -steps
	}

	return c.get(ctx, "/main/setVolume", url.Values{
		"volume": {direction},
		"step":   {strconv.Itoa(magnitude)},
	}, nil)
}
