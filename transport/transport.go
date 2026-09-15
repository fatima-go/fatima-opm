// Package transport keeps legacy HTTP handlers and native gRPC on one port.
package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fatima-go/fatima-opm/api"
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const CapabilityPath = "/.well-known/fatima/capabilities"
const TokenHeader = "fatima-auth-token"

var ErrLegacy = errors.New("server does not support the progressive deployment API")

func ID(prefix string) string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return prefix + hex.EncodeToString(b)
}
func Token(ctx context.Context) string {
	m, _ := metadata.FromIncomingContext(ctx)
	v := m.Get(TokenHeader)
	if len(v) > 0 {
		return v[0]
	}
	return ""
}
func WithToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, TokenHeader, token)
}

type discovery struct {
	api.UnimplementedDiscoveryServer
	caps *api.Capabilities
}

func (d *discovery) GetCapabilities(context.Context, *api.Empty) (*api.Capabilities, error) {
	return d.caps, nil
}

type Server struct {
	HTTP     *http.Server
	GRPC     *grpc.Server
	once     sync.Once
	listener net.Listener
	mu       sync.Mutex
	closed   bool
}

func NewServer(h *http.Server, g *grpc.Server, caps *api.Capabilities) *Server {
	api.RegisterDiscoveryServer(g, &discovery{caps: caps})
	original := h.Handler
	if original == nil {
		original = http.DefaultServeMux
	}
	h.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == CapabilityPath && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			json.NewEncoder(w).Encode(caps)
			return
		}
		original.ServeHTTP(w, r)
	})
	return &Server{HTTP: h, GRPC: g}
}

func (s *Server) ListenAndServe() error {
	l, e := net.Listen("tcp", s.HTTP.Addr)
	if e != nil {
		return e
	}
	return s.Serve(l)
}
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		l.Close()
		return http.ErrServerClosed
	}
	s.listener = l
	s.mu.Unlock()
	mux := cmux.New(l)
	// Match the HTTP/2 preface, not headers that require a SETTINGS exchange.
	h2 := mux.Match(cmux.HTTP2())
	h1 := mux.Match(cmux.Any())
	if s.HTTP.ReadTimeout > 0 {
		mux.SetReadTimeout(s.HTTP.ReadTimeout)
	}
	errs := make(chan error, 2)
	go func() { errs <- s.GRPC.Serve(h2); _ = l.Close() }()
	go func() { errs <- s.HTTP.Serve(h1); _ = l.Close() }()
	e := mux.Serve()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return http.ErrServerClosed
	}
	select {
	case inner := <-errs:
		if inner != nil {
			return inner
		}
	default:
	}
	return e
}

func (s *Server) Shutdown(ctx context.Context) error {
	var result error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.listener != nil {
			s.listener.Close()
		}
		s.mu.Unlock()
		done := make(chan struct{})
		go func() { s.GRPC.GracefulStop(); close(done) }()
		result = s.HTTP.Shutdown(ctx)
		if errors.Is(result, net.ErrClosed) {
			result = nil
		}
		select {
		case <-done:
		case <-ctx.Done():
			s.GRPC.Stop()
			result = ctx.Err()
		}
	})
	return result
}

func Address(endpoint string) (string, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" {
		return "", fmt.Errorf("invalid endpoint")
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("unsupported endpoint scheme %s", u.Scheme)
	}
	return u.Host, nil
}

func Dial(endpoint string) (*grpc.ClientConn, error) {
	address, e := Address(endpoint)
	if e != nil {
		return nil, e
	}
	return grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16*1024*1024)))
}

// Discover is read-only. Only an explicit absence/incompatible API means legacy;
// authorization/network errors must never trigger a second mutating request.
func Discover(ctx context.Context, endpoint string) (*api.Capabilities, error) {
	addr, e := Address(endpoint)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+CapabilityPath, nil)
	if e != nil {
		return nil, e
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 405 || resp.StatusCode == 501 {
		return nil, ErrLegacy
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("capability lookup: HTTP %d", resp.StatusCode)
	}
	var c api.Capabilities
	if e = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&c); e != nil {
		return nil, fmt.Errorf("invalid capability response: %w", e)
	}
	if c.ApiVersion != 2 {
		return nil, ErrLegacy
	}
	conn, e := Dial(endpoint)
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	actual, e := api.NewDiscoveryClient(conn).GetCapabilities(check, &api.Empty{})
	if e != nil {
		return nil, e
	}
	if actual.ApiVersion != 2 || actual.Server != c.Server {
		return nil, fmt.Errorf("server capabilities changed; retry discovery")
	}
	return actual, nil
}

func Supports(c *api.Capabilities, feature string) bool {
	if c == nil {
		return false
	}
	for _, f := range c.Features {
		if f == feature {
			return true
		}
	}
	return false
}
