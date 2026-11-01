package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Protocol is the gateway's WebSocket subprotocol.
const Protocol = "hermes-gateway-v1"

// ticketPrefix prefixes the single-use ticket offered as a second
// subprotocol.
const ticketPrefix = "hermes-gateway-ticket."

// Endpoint is a running gateway, as `hermes gateway ensure --json` reports
// it.
type Endpoint struct {
	ProfileID       string   `json:"profile_id"`
	InstanceID      string   `json:"instance_id"`
	AuthorityEpoch  int64    `json:"authority_epoch"`
	RuntimeProtocol int      `json:"runtime_protocol"`
	APIOrigin       string   `json:"api_origin"`
	Capabilities    []string `json:"capabilities"`
	ControlHome     string   `json:"control_home"`
	CodeSHA         string   `json:"code_sha"`
}

type ensureResult struct {
	State      string    `json:"state"`
	ReasonCode string    `json:"reason_code"`
	Detail     string    `json:"detail"`
	Endpoint   *Endpoint `json:"endpoint"`
}

// Ensure starts the gateway of the Hermes home the environment names (or
// finds the one running) and returns its endpoint.
func Ensure(ctx context.Context, bin string, env []string) (Endpoint, error) {
	cmd := exec.CommandContext(ctx, bin, "gateway", "ensure", "--json")
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var res ensureResult
	if err := json.Unmarshal(bytes.TrimSpace(lastLine(out)), &res); err != nil {
		return Endpoint{}, fmt.Errorf("hermes: gateway ensure: %w (%s)", errors.Join(runErr, err), strings.TrimSpace(stderr.String()))
	}
	if runErr != nil {
		return Endpoint{}, fmt.Errorf("hermes: gateway ensure: %w (%s %s %s)", runErr, res.State, res.ReasonCode, strings.TrimSpace(stderr.String()))
	}
	if res.State != "ready" || res.Endpoint == nil {
		return Endpoint{}, fmt.Errorf("hermes: gateway is %s: %s %s", res.State, res.ReasonCode, res.Detail)
	}
	ep := *res.Endpoint
	if ep.ControlHome == "" {
		ep.ControlHome = Home(env)
	}
	if ep.RuntimeProtocol != 1 {
		return Endpoint{}, fmt.Errorf("hermes: gateway runtime protocol %d, want 1", ep.RuntimeProtocol)
	}
	u, err := url.Parse(ep.APIOrigin)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") {
		return Endpoint{}, fmt.Errorf("hermes: gateway origin %q is not loopback", ep.APIOrigin)
	}
	return ep, nil
}

// Home is the Hermes home an environment names: HERMES_HOME, else
// ~/.hermes.
func Home(env []string) string {
	home := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			switch k {
			case "HERMES_HOME":
				if v != "" {
					return v
				}
			case "HOME":
				home = v
			}
		}
	}
	return filepath.Join(home, ".hermes")
}

func lastLine(b []byte) []byte {
	b = bytes.TrimRight(b, "\n")
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		return b[i+1:]
	}
	return b
}

// controlSocket is where the gateway of home listens for control requests:
// home/gateway.sock, or the path its pointer file names when that is too
// long for a Unix socket.
func controlSocket(home string) (string, error) {
	direct := filepath.Join(home, "gateway.sock")
	if _, err := os.Stat(direct); err == nil {
		return direct, nil
	}
	b, err := fs.ReadFile(os.DirFS(home), "gateway.sock.path")
	if err != nil {
		return "", fmt.Errorf("hermes: no gateway control socket in %s: %w", home, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Ticket mints a single-use, short-lived ticket for one WebSocket
// connection over the gateway's control socket.
func Ticket(ctx context.Context, ep Endpoint) (string, error) {
	path, err := controlSocket(ep.ControlHome)
	if err != nil {
		return "", err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return "", fmt.Errorf("hermes: control socket: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	}
	req, err := json.Marshal(map[string]any{"protocol": 1, "id": 1, "verb": "session-ticket", "params": map[string]string{
		"profile_id": ep.ProfileID, "instance_id": ep.InstanceID, "purpose": "interactive",
	}})
	if err != nil {
		return "", err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return "", fmt.Errorf("hermes: control socket: %w", err)
	}
	line, err := bufio.NewReaderSize(conn, 64<<10).ReadBytes('\n')
	if err != nil {
		return "", fmt.Errorf("hermes: control socket: %w", err)
	}
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Ticket     string `json:"ticket"`
			InstanceID string `json:"instance_id"`
			ProfileID  string `json:"profile_id"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", fmt.Errorf("hermes: ticket response: %w", err)
	}
	if !resp.OK || resp.Result.Ticket == "" {
		return "", fmt.Errorf("hermes: ticket refused: %s", resp.Error)
	}
	if resp.Result.InstanceID != ep.InstanceID || resp.Result.ProfileID != ep.ProfileID {
		return "", errors.New("hermes: the gateway changed while minting a ticket")
	}
	return resp.Result.Ticket, nil
}

// Dial opens a WebSocket to the gateway with a fresh ticket. It sends no
// Origin header (the gateway refuses one), never goes through a proxy and
// never follows a redirect.
func Dial(ctx context.Context, ep Endpoint) (*websocket.Conn, error) {
	t, err := Ticket(ctx, ep)
	if err != nil {
		return nil, err
	}
	u := strings.Replace(ep.APIOrigin, "http://", "ws://", 1) + "/api/ws"
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		Subprotocols: []string{Protocol, ticketPrefix + t},
		HTTPClient: &http.Client{
			Transport:     &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	})
	if err != nil {
		return nil, fmt.Errorf("hermes: dial gateway: %w", err)
	}
	if c.Subprotocol() != Protocol {
		_ = c.Close(websocket.StatusProtocolError, "")
		return nil, fmt.Errorf("hermes: gateway speaks %q, want %q", c.Subprotocol(), Protocol)
	}
	c.SetReadLimit(8 << 20)
	return c, nil
}
