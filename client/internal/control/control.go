// Package control is the Client's local control socket, the carrier of the
// External trigger. A compositor keybind or script runs `kitsune-client toggle`,
// which sends a command over the socket; the running Client turns it into a
// Dictation-cycle trigger.
//
// The wire protocol is one newline-terminated command per connection: a command
// name (toggle, start, stop, cancel) and a single-line response, "ok" or an
// "error: ..." message.
package control

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
)

const (
	// network is the transport for the control socket. Unix domain sockets work
	// on every platform the Client supports, including Windows.
	network = "unix"
	// ioTimeout bounds a single request/response exchange.
	ioTimeout = 5 * time.Second
	// responseOK is the successful response line.
	responseOK = "ok"
)

// Command is an External-trigger command name.
type Command string

const (
	// Toggle starts or stops the Dictation cycle.
	Toggle Command = "toggle"
	// Start begins recording.
	Start Command = "start"
	// Stop ends recording and processes the utterance.
	Stop Command = "stop"
	// Cancel discards the in-progress utterance.
	Cancel Command = "cancel"
)

// ParseCommand maps a wire command to the Trigger it produces.
func ParseCommand(s string) (cycle.Trigger, error) {
	switch Command(strings.ToLower(strings.TrimSpace(s))) {
	case Toggle:
		return cycle.Toggle, nil
	case Start:
		return cycle.Start, nil
	case Stop:
		return cycle.Stop, nil
	case Cancel:
		return cycle.Cancel, nil
	default:
		return 0, fmt.Errorf("unknown command %q", strings.TrimSpace(s))
	}
}

// Deliver receives a Trigger produced by a control command.
type Deliver func(cycle.Trigger)

// Server accepts control connections and delivers their triggers.
type Server struct {
	ln net.Listener
}

// NewServer wraps an already-bound listener.
func NewServer(ln net.Listener) *Server { return &Server{ln: ln} }

// Serve accepts connections until ctx is canceled or the listener is closed,
// delivering each valid command. It returns nil on a clean shutdown.
func (s *Server) Serve(ctx context.Context, deliver Deliver) error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.ln.Close()
		case <-stop:
		}
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept control connection: %w", err)
		}
		s.handle(conn, deliver)
	}
}

func (s *Server) handle(conn net.Conn, deliver Deliver) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	trigger, err := ParseCommand(line)
	if err != nil {
		_, _ = fmt.Fprintf(conn, "error: %v\n", err)
		return
	}
	deliver(trigger)
	_, _ = fmt.Fprintln(conn, responseOK)
}

// Listen binds a control socket at path, replacing a stale socket left by a
// previous run. It refuses to clobber a non-socket file.
func Listen(path string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("control socket path %s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale control socket %s: %w", path, err)
		}
	}
	return net.Listen(network, path)
}

// Send dials a running control socket and sends one command, returning an error
// unless the server answers "ok".
func Send(addr, command string) error {
	conn, err := net.Dial(network, addr)
	if err != nil {
		return fmt.Errorf("connect to the client control socket %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))

	if _, err := fmt.Fprintln(conn, command); err != nil {
		return fmt.Errorf("send control command: %w", err)
	}
	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && response == "" {
		return fmt.Errorf("read control response: %w", err)
	}
	if strings.TrimSpace(response) != responseOK {
		return fmt.Errorf("control command rejected: %s", strings.TrimSpace(response))
	}
	return nil
}
