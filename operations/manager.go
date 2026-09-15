// Package operations persists request identities before invoking a side effect.
// A disconnected caller can observe the same operation without resubmitting it.
package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fatima-go/fatima-opm/api"
	"github.com/fatima-go/fatima-opm/artifact"
	"github.com/fatima-go/fatima-opm/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Emit func(stage, state, message string, current, total int64) error
type Execute func(context.Context, Emit) (string, string, error)
type record struct {
	Hash      string
	Operation *api.ControlOperation
}
type database struct{ Records map[string]*record }
type Manager struct {
	store     *store.Store
	packageID string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closing   bool
	wg        sync.WaitGroup
	lock      *os.File
	failureMu sync.RWMutex
	failures  map[string]error
}

func New(root, packageID string) (*Manager, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(root, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, err
	}
	m := &Manager{store: st, packageID: packageID, lock: lock, failures: map[string]error{}}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	var db database
	err = st.Update("requests", &db, func() error {
		for _, r := range db.Records {
			if !Terminal(r.Operation.State) {
				appendEvent(r.Operation, "recovery", "INTERRUPTED", "Server restarted; inspect the target before submitting a new request", 0, 0)
				r.Operation.FinishedAt = time.Now().Unix()
			}
		}
		return nil
	})
	if err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closing = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	syscall.Flock(int(m.lock.Fd()), syscall.LOCK_UN)
	m.lock.Close()
}
func (m *Manager) Done() <-chan struct{} { return m.ctx.Done() }
func Terminal(state string) bool {
	switch state {
	case "REQUESTED", "SUCCEEDED", "FAILED", "INTERRUPTED":
		return true
	}
	return false
}
func appendEvent(o *api.ControlOperation, stage, state, message string, current, total int64) {
	if stage == "request" || stage == "result" || stage == "recovery" {
		o.State = state
	}
	o.Message = message
	o.Events = append(o.Events, &api.Event{Sequence: uint64(len(o.Events) + 1), At: time.Now().Unix(), Stage: stage, State: state, Message: message, Current: current, Total: total})
}
func (m *Manager) Get(id string) (*api.ControlOperation, error) {
	if !artifact.SafeName.MatchString(id) {
		return nil, status.Error(codes.InvalidArgument, "invalid request ID")
	}
	if err := m.journalFailure(id); err != nil {
		return nil, err
	}
	var db database
	if err := m.store.Update("requests", &db, nil); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	r := db.Records[id]
	if r == nil {
		return nil, status.Error(codes.NotFound, "operation not found")
	}
	return r.Operation, nil
}
func (m *Manager) Start(id, kind string, request proto.Message, execute Execute) (*api.ControlOperation, error) {
	if !artifact.SafeName.MatchString(id) || len(id) > 128 {
		return nil, status.Error(codes.InvalidArgument, "request_id is required (maximum 128 safe characters)")
	}
	bytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte(kind+"\x00"), bytes...))
	hash := hex.EncodeToString(sum[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, status.Error(codes.Unavailable, "server is shutting down")
	}
	if err := m.journalFailure(id); err != nil {
		return nil, err
	}
	created := false
	var op *api.ControlOperation
	var db database
	err = m.store.Update("requests", &db, func() error {
		if db.Records == nil {
			db.Records = map[string]*record{}
		}
		if r := db.Records[id]; r != nil {
			if r.Hash != hash {
				return status.Error(codes.AlreadyExists, "request ID already used with different input")
			}
			op = r.Operation
			return nil
		}
		op = &api.ControlOperation{Id: id, Kind: kind, PackageId: m.packageID, StartedAt: time.Now().Unix()}
		appendEvent(op, "request", "PENDING", "Request accepted", 0, 0)
		db.Records[id] = &record{Hash: hash, Operation: op}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	snapshot := proto.Clone(op).(*api.ControlOperation)
	if created {
		m.wg.Add(1)
		go func() { defer m.wg.Done(); m.run(id, execute) }()
	}
	return snapshot, nil
}
func (m *Manager) run(id string, execute Execute) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Minute)
	defer cancel()
	emit := func(stage, state, message string, current, total int64) error {
		var db database
		err := m.store.Update("requests", &db, func() error {
			r := db.Records[id]
			if r == nil {
				return fmt.Errorf("missing operation")
			}
			appendEvent(r.Operation, stage, state, message, current, total)
			if stage == "result" && Terminal(state) {
				r.Operation.FinishedAt = time.Now().Unix()
			}
			return nil
		})
		if err != nil {
			err = status.Errorf(codes.DataLoss, "cannot persist operation %s; inspect the target and restore storage before retrying: %v", id, err)
			m.failureMu.Lock()
			m.failures[id] = err
			m.failureMu.Unlock()
		}
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			_ = emit("result", "FAILED", fmt.Sprintf("operation panic: %v", r), 0, 0)
		}
	}()
	if err := emit("request", "RUNNING", "Processing request", 0, 0); err != nil {
		return
	}
	state, message, err := execute(ctx, emit)
	if err != nil {
		state = "FAILED"
		message = err.Error()
		if ctx.Err() != nil {
			state = "INTERRUPTED"
		}
	}
	if !Terminal(state) {
		state = "FAILED"
		message = "executor did not provide a terminal result"
	}
	_ = emit("result", state, message, 0, 0)
}
func (m *Manager) journalFailure(id string) error {
	m.failureMu.RLock()
	defer m.failureMu.RUnlock()
	return m.failures[id]
}
func (m *Manager) Watch(ctx context.Context, id string, send func(*api.ControlOperation) error) error {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var sequence int
	for {
		o, err := m.Get(id)
		if err != nil {
			return err
		}
		if len(o.Events) != sequence {
			if err = send(o); err != nil {
				return err
			}
			sequence = len(o.Events)
		}
		if Terminal(o.State) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.ctx.Done():
			return status.Error(codes.Unavailable, "server is shutting down")
		case <-tick.C:
		}
	}
}
