package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fatima-go/fatima-opm/api"
	"google.golang.org/grpc"
)

func TestSamePortPreservesHTTPAndNativeGRPC(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	h := &http.Server{ReadTimeout: 100 * time.Millisecond, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Error("legacy protocol changed")
		}
		w.Header().Set("X-Legacy", "unchanged")
		w.WriteHeader(207)
		io.Copy(w, r.Body)
	})}
	s := NewServer(h, grpc.NewServer(), &api.Capabilities{Server: "test", ApiVersion: 2})
	done := make(chan error, 1)
	go func() { done <- s.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := s.Shutdown(ctx); e != nil {
			t.Error(e)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("listener did not stop")
		}
	})
	endpoint := "http://" + listener.Addr().String()
	conn, e := Dial(endpoint)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	for i := 0; i < 3; i++ {
		resp, e := http.Post(endpoint+"/old/path?query=1", "application/octet-stream", strings.NewReader("legacy body"))
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 207 || resp.Header.Get("X-Legacy") != "unchanged" || string(b) != "legacy body" {
			t.Fatal("legacy response changed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		caps, e := api.NewDiscoveryClient(conn).GetCapabilities(ctx, &api.Empty{})
		cancel()
		if e != nil || caps.ApiVersion != 2 {
			t.Fatalf("native gRPC: %v", e)
		}
		time.Sleep(150 * time.Millisecond) // Beyond old HTTP ReadTimeout on same connection.
	}
	if _, e = Discover(context.Background(), endpoint); e != nil {
		t.Fatal(e)
	}
}
func TestOnlyExplicitAbsenceFallsBack(t *testing.T) {
	for _, code := range []int{404, 405, 501, 401, 403, 500, 502} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer server.Close()
			_, e := Discover(context.Background(), server.URL)
			want := code == 404 || code == 405 || code == 501
			if errors.Is(e, ErrLegacy) != want {
				t.Fatalf("status %d treated as legacy: %v", code, e)
			}
		})
	}
	_, e := Discover(context.Background(), "http://127.0.0.1:1")
	if e == nil || errors.Is(e, ErrLegacy) {
		t.Fatal("network failure must not fall back")
	}
}
