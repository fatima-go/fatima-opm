package operations

import (
	"context"
	"github.com/fatima-go/fatima-opm/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestReplayAndWatch(t *testing.T) {
	root := t.TempDir()
	m, err := New(root, "host:default")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	gate := make(chan struct{})
	exec := func(ctx context.Context, emit Emit) (string, string, error) {
		calls.Add(1)
		emit("delivery", "SUCCEEDED", "delivered", 1, 1)
		<-gate
		return "REQUESTED", "not a batch completion", nil
	}
	q := &api.CronRequest{RequestId: "same", Process: "sample", Job: "run"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Start("same", "rocron", q, exec); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	time.Sleep(20 * time.Millisecond)
	op, _ := m.Get("same")
	if Terminal(op.State) {
		t.Fatal("stage completion became operation completion")
	}
	changed := &api.CronRequest{RequestId: "same", Process: "other", Job: "run"}
	if _, err = m.Start("same", "rocron", changed, exec); status.Code(err) != codes.AlreadyExists {
		t.Fatal(err)
	}
	close(gate)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = m.Watch(ctx, "same", func(o *api.ControlOperation) error { op = o; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || op.State != "REQUESTED" {
		t.Fatalf("calls=%d state=%s", calls.Load(), op.State)
	}
	m.Close()
	m, err = New(root, "host:default")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Start("same", "rocron", q, exec); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("replayed after restart")
	}
}
