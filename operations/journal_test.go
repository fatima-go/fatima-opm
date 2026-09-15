package operations

import (
	"context"
	"github.com/fatima-go/fatima-opm/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"testing"
)

func TestJournalFailureEndsObservation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission fault requires an unprivileged user")
	}
	root := t.TempDir()
	m, err := New(root, "test:default")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer os.Chmod(root, 0700)
	started := make(chan struct{})
	finish := make(chan struct{})
	_, err = m.Start("journal-fault", "test", &api.Empty{}, func(context.Context, Emit) (string, string, error) {
		close(started)
		<-finish
		return "SUCCEEDED", "done", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	close(finish)
	m.wg.Wait()
	if _, err = m.Get("journal-fault"); status.Code(err) != codes.DataLoss {
		t.Fatal("unrecorded result looked like an ongoing operation", err)
	}
	if err = m.Watch(context.Background(), "journal-fault", func(*api.ControlOperation) error { return nil }); status.Code(err) != codes.DataLoss {
		t.Fatal(err)
	}
}
