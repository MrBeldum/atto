package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"atto/config"
)

// ServeStdio speaks JSON-RPC as JSON lines on r/w (codex app-server style):
// one request per line in, responses and notifications out.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	var mu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write(append(b, '\n'))
	}
	s.Notify = func(method string, params map[string]any) {
		write(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if resp := s.Handle(ctx, []byte(line)); resp != nil {
			write(resp)
		}
	}
	return sc.Err()
}

// broker fans notifications out to SSE subscribers and keeps a ring of
// recent events so a client that reconnects (e.g. a phone waking up) can
// resume with Last-Event-ID.
type broker struct {
	mu   sync.Mutex
	seq  int64
	ring []sseEvent
	subs map[chan sseEvent]struct{}
	keep int
}

type sseEvent struct {
	id   int64
	data []byte
}

func newBroker(keep int) *broker {
	return &broker{subs: map[chan sseEvent]struct{}{}, keep: keep}
}

func (b *broker) publish(v any) {
	data, _ := json.Marshal(v)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	ev := sseEvent{b.seq, data}
	b.ring = append(b.ring, ev)
	if len(b.ring) > b.keep {
		b.ring = b.ring[len(b.ring)-b.keep:]
	}
	for ch := range b.subs {
		select {
		case ch <- ev:
		default: // slow client: drop; it can resume from the ring
		}
	}
}

// subscribe returns events after lastID (replayed from the ring) and a
// channel of new ones.
func (b *broker) subscribe(lastID int64) ([]sseEvent, chan sseEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var backlog []sseEvent
	for _, ev := range b.ring {
		if ev.id > lastID {
			backlog = append(backlog, ev)
		}
	}
	ch := make(chan sseEvent, 1024)
	b.subs[ch] = struct{}{}
	return backlog, ch
}

func (b *broker) unsubscribe(ch chan sseEvent) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

//go:embed web/index.html
var webFS embed.FS

// TokenPath stores the HTTP server's bearer token.
func TokenPath() string { return filepath.Join(config.Dir(), "server-token") }

// LoadOrCreateToken returns the persistent server token.
func LoadOrCreateToken() (string, error) {
	if b, err := os.ReadFile(TokenPath()); err == nil && len(strings.TrimSpace(string(b))) >= 16 {
		return strings.TrimSpace(string(b)), nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		return "", err
	}
	return tok, os.WriteFile(TokenPath(), []byte(tok+"\n"), 0o600)
}

// HTTPHandler serves the protocol over HTTP:
//
//	POST /rpc      one JSON-RPC request in the body, response in the reply
//	GET  /events   notifications as Server-Sent Events (resumable)
//	GET  /         a minimal web client
//
// Every request except "/" needs the token, as "Authorization: Bearer"
// or ?token= (EventSource cannot set headers).
func (s *Server) HTTPHandler(token string) http.Handler {
	b := newBroker(10000)
	s.Notify = func(method string, params map[string]any) {
		b.publish(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	}
	authed := func(r *http.Request) bool {
		got := r.URL.Query().Get("token")
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got = strings.TrimPrefix(h, "Bearer ")
		}
		return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		page, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("POST /rpc", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := s.Handle(r.Context(), body)
		w.Header().Set("Content-Type", "application/json")
		if resp == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		last, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
		if q := r.URL.Query().Get("lastEventId"); q != "" {
			last, _ = strconv.ParseInt(q, 10, 64)
		}
		backlog, ch := b.subscribe(last)
		defer b.unsubscribe(ch)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		write := func(ev sseEvent) {
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.id, ev.data)
		}
		for _, ev := range backlog {
			write(ev)
		}
		fl.Flush()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case ev := <-ch:
				write(ev)
				fl.Flush()
			case <-heartbeat.C:
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			}
		}
	})
	return mux
}

// IsLoopback reports whether addr only listens on the local machine.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
