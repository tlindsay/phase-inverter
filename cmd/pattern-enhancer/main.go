// Command pattern-enhancer proxies a Yamaha MusicCast receiver onto a tailnet.
//
// It exists because the receiver cannot join the tailnet itself and the laptop
// driving it is not always on the receiver's LAN. Something with a foot in both
// networks has to translate, and this is it: MusicCast's HTTP + UDP protocol on
// the LAN side, a small REST + SSE contract on the tailnet side.
//
// It replaces an MQTT broker plus a protocol bridge that previously ran on a
// Raspberry Pi, and fixes that stack's central flaw — it had no way to confirm
// a command had taken effect, and computed absolute volume targets from a cache
// that went stale whenever anyone touched the physical knob.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"tailscale.com/tsnet"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/musiccast"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/server"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/state"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("pattern-enhancer: %v", err)
	}
}

func run() error {
	var (
		receiver = flag.String("receiver", "",
			"host or IP of the MusicCast receiver (required)")
		deviceID = flag.String("device", "office",
			"device id used in API paths")
		tsHostname = flag.String("ts-hostname", "pattern-enhancer",
			"node name to claim on the tailnet")
		stateDir = flag.String("state-dir", "",
			"directory for tsnet node state (required unless -local)")
		localAddr = flag.String("local", "",
			"listen on this TCP address instead of joining the tailnet; for development")
		pollInterval = flag.Duration("poll", 30*time.Second,
			"how often to reconcile against the receiver with a full getStatus")
		udpPort = flag.Int("udp-port", 0,
			"UDP port to receive receiver events on (0 picks an ephemeral port)")
		verbose = flag.Bool("v", false,
			"log at debug level: every pushed event and reconcile, plus tsnet's internal chatter; useful for confirming the UDP subscription is alive")
		authKeyFile = flag.String("auth-key-file", "",
			"file holding a Tailscale auth key; falls back to TS_AUTHKEY, then to an interactive login URL")
		healthAddr = flag.String("health-addr", "",
			"additionally serve the API on this loopback address, for local health checks and debugging")
	)
	flag.Parse()

	if *verbose {
		log.SetLevel(log.DebugLevel)
	}

	if *receiver == "" {
		return errors.New("-receiver is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The UDP socket must exist before the first HTTP request: its port is
	// what gets advertised in X-AppPort, and that advertisement is what makes
	// the receiver push events at all.
	listener, err := musiccast.Listen(*udpPort)
	if err != nil {
		return fmt.Errorf("binding UDP event socket: %w", err)
	}
	defer listener.Close()
	log.Infof("listening for receiver events on UDP port %d", listener.Port())

	mc := musiccast.NewClient(*receiver, listener.Port())

	// Deliberately no startup handshake with the receiver.
	//
	// This daemon runs as a system service on a node that reboots on its own
	// schedule, and the receiver drops off the network entirely once it has
	// been in standby for a while. Requiring it to answer before serving would
	// mean the service fails at boot every time the stereo is off overnight,
	// and systemd would sit there restarting it into the same wall.
	//
	// So it comes up immediately and converges: the volume scale and current
	// state are both filled in by the reconcile loop as soon as the receiver
	// answers. Until then State reports Online false with no range, which
	// clients render as unavailable rather than as a stale volume.
	store := state.NewStore(pe.Range{})

	// Wiring order: the server needs a Backend, and the Device needs somewhere
	// to publish. srv is captured by the closure, which is safe because Run
	// does not start until below.
	var srv *server.Server
	device := state.NewDevice(store, mc, func(s pe.State) {
		srv.Publish(s)
	})
	srv = server.New(*deviceID, device)
	srv.StatsFunc = func() any { return device.Stats() }

	go device.Run(ctx, listener.Events(ctx), *pollInterval)

	ln, cleanup, err := listen(ctx, *localAddr, *tsHostname, *stateDir, *authKeyFile)
	if err != nil {
		return err
	}
	defer cleanup()

	httpSrv := &http.Server{
		Handler: srv.Handler(),
		// No write timeout: SSE connections are meant to stay open, and any
		// deadline here would sever them on a fixed schedule.
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	// An optional second listener on loopback.
	//
	// When serving over tsnet the API exists only on the tailnet, which leaves
	// anything running on this host — a monitoring heartbeat, a shell session
	// debugging a problem — with no way to reach it except by routing back out
	// through the tailnet to itself. Loopback-only, so it exposes nothing that
	// was not already exposed.
	if *healthAddr != "" {
		healthLn, err := net.Listen("tcp", *healthAddr)
		if err != nil {
			return fmt.Errorf("listening on health address %s: %w", *healthAddr, err)
		}
		defer healthLn.Close()

		healthSrv := &http.Server{
			Handler:           srv.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = healthSrv.Shutdown(shutdownCtx)
		}()
		go func() {
			if err := healthSrv.Serve(healthLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Errorf("health listener stopped: %v", err)
			}
		}()
		log.Infof("also serving on %s for local health checks", healthLn.Addr())
	}

	log.Infof("serving device %q for receiver %s on %s",
		*deviceID, *receiver, ln.Addr())

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// listen returns the socket the API is served on.
//
// The tailnet path uses tsnet, which makes the daemon its own node rather than
// a port on its host. That gets it an identity ACLs can target and keeps it
// off the LAN entirely — the receiver's own network has no route to it.
//
// -local exists for development against a real receiver without joining the
// tailnet at all.
func listen(ctx context.Context, localAddr, hostname, stateDir, authKeyFile string) (net.Listener, func(), error) {
	if localAddr != "" {
		ln, err := net.Listen("tcp", localAddr)
		return ln, func() {}, err
	}

	if stateDir == "" {
		return nil, nil, errors.New("-state-dir is required when joining the tailnet")
	}

	authKey, err := readAuthKey(authKeyFile)
	if err != nil {
		return nil, nil, err
	}

	ts := &tsnet.Server{
		Hostname: hostname,
		Dir:      stateDir,
		AuthKey:  authKey,

		// Two separate log sinks, and the distinction matters. Logf is
		// tsnet's internal chatter, which is voluminous enough to bury
		// anything useful, so it lands at debug and stays hidden without -v.
		// UserLogf carries messages meant for a human — most importantly the
		// login URL printed when there is no auth key and no saved node
		// state. Silencing both would make a first run look like a hang.
		Logf:     log.Debugf,
		UserLogf: log.Infof,
	}

	status, err := ts.Up(ctx)
	if err != nil {
		ts.Close()
		return nil, nil, fmt.Errorf("joining tailnet as %q: %w", hostname, err)
	}
	if status != nil && len(status.TailscaleIPs) > 0 {
		log.Infof("joined tailnet as %q (%s)", status.Self.DNSName, status.TailscaleIPs[0])
	}

	// Port 80 on the daemon's own tailnet address, not the host's. Nothing
	// else is listening there, and the tailnet carries its own encryption.
	ln, err := ts.Listen("tcp", ":80")
	if err != nil {
		ts.Close()
		return nil, nil, fmt.Errorf("listening on tailnet: %w", err)
	}

	return ln, func() { ln.Close(); ts.Close() }, nil
}

// readAuthKey loads a Tailscale auth key from a file.
//
// A file rather than a flag or an environment variable because that is how
// secrets arrive on the deployment target: agenix decrypts to a path, and a
// key passed as an argument would be visible in the process table to every
// user on the box. An empty path is allowed — tsnet falls back to TS_AUTHKEY,
// and failing that prints a login URL for a one-time interactive auth.
func readAuthKey(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading auth key from %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}
