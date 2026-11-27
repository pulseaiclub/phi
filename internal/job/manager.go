package job

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/pulseaiclub/phi/internal/core"
)

// Option configures a [Manager] at construction.
type Option func(*managerConfig)

type managerConfig struct {
	maxConcurrent      int
	progressBufferSize int
	roleModels         map[Role]core.ModelConfig
	recovery           RecoveryMode
	onStoreError       func(op, jobID string, err error)
	store              Store
}

// WithMaxConcurrent limits simultaneous jobs. Non-positive limits use the default of 4.
func WithMaxConcurrent(limit int) Option {
	return func(c *managerConfig) { c.maxConcurrent = limit }
}

// WithSubscribeBufferSize sets the buffer size for progress subscriptions. Non-positive sizes use the default of 64.
func WithSubscribeBufferSize(size int) Option {
	return func(c *managerConfig) { c.progressBufferSize = size }
}

// WithRoleModels sets the initial models for child jobs.
func WithRoleModels(models map[Role]core.ModelConfig) Option {
	return func(c *managerConfig) { c.roleModels = models }
}

// WithRecovery selects how New handles non-terminal jobs left on disk.
func WithRecovery(mode RecoveryMode) Option {
	return func(c *managerConfig) { c.recovery = mode }
}

// WithOnStoreError reports store errors that cannot be returned to the caller.
func WithOnStoreError(fn func(op, jobID string, err error)) Option {
	return func(c *managerConfig) { c.onStoreError = fn }
}

// WithStore replaces the default filesystem-backed job store.
func WithStore(store Store) Option {
	return func(c *managerConfig) { c.store = store }
}

// Manager owns in-process job lifecycles and a disk store.
type Manager struct {
	store              Store
	runner             Runner
	maxConcurrent      int
	progressBufferSize int
	onStoreError       func(op, jobID string, err error)

	mu         sync.Mutex
	roleModels map[Role]core.ModelConfig
	closed     bool
	slots      chan struct{}
	jobs       map[string]*liveJob
	subs       []*subscriber
}

type liveJob struct {
	meta            Meta
	parentToolUseID string
	parentContext   ParentContext
	model           core.ModelConfig
	cancel          context.CancelFunc
	done            chan struct{}
}

// subscriber is one progress channel. closed is guarded by m.mu: emitProgress
// sends while holding m.mu, and cancel/Close mark closed and close the channel
// under the same lock, so a send can never race a channel close.
type subscriber struct {
	ch     chan Progress
	closed bool
}

// New creates a Manager for the given jobs directory and runner.
//
// By default ([RecoverMarkFailed]), leftover starting/running jobs on disk are
// marked failed so a process restart does not leave Wait/Cancel zombies.
func New(root string, runner Runner, opts ...Option) (*Manager, error) {
	if runner == nil {
		return nil, fmt.Errorf("%w: Runner is required", ErrInvalid)
	}
	var cfg managerConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxConcurrent <= 0 {
		cfg.maxConcurrent = 4
	}
	if cfg.progressBufferSize <= 0 {
		cfg.progressBufferSize = 64
	}
	if cfg.store == nil {
		var err error
		cfg.store, err = newFileStore(root)
		if err != nil {
			return nil, err
		}
	}
	m := &Manager{
		store:              cfg.store,
		runner:             runner,
		maxConcurrent:      cfg.maxConcurrent,
		progressBufferSize: cfg.progressBufferSize,
		onStoreError:       cfg.onStoreError,
		roleModels:         maps.Clone(cfg.roleModels),
		slots:              make(chan struct{}, cfg.maxConcurrent),
		jobs:               make(map[string]*liveJob),
	}
	if cfg.recovery != RecoverIgnore {
		if err := m.recoverStale(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) recoverStale() error {
	ids, err := m.store.ListIDs()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, id := range ids {
		meta, err := m.store.Load(id)
		if err != nil {
			m.reportStore("readMeta", id, err)
			continue
		}
		if meta.Status.Terminal() {
			continue
		}
		meta.Status = StatusFailed
		meta.FinishedAt = now
		meta.Error = "interrupted: process restarted before job finished"
		if err := m.store.Save(meta); err != nil {
			return fmt.Errorf("job: recover %s: %w", id, err)
		}
		if err := m.store.AppendEvent(id, "recovered: marked failed after restart"); err != nil {
			m.reportStore("appendEvent", id, err)
		}
	}
	return nil
}

func (m *Manager) reportStore(op, jobID string, err error) {
	if err == nil || m.onStoreError == nil {
		return
	}
	m.onStoreError(op, jobID, err)
}

func (m *Manager) persistMeta(meta Meta) {
	if err := m.store.Save(meta); err != nil {
		m.reportStore("writeMeta", meta.ID, err)
	}
}

func (m *Manager) persistEvent(meta Meta, msg string) {
	if err := m.store.AppendEvent(meta.ID, msg); err != nil {
		m.reportStore("appendEvent", meta.ID, err)
	}
}

// SetRoleModels replaces the models used by future jobs.
// Configs and their nested values must be treated as immutable after this call.
func (m *Manager) SetRoleModels(models map[Role]core.ModelConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roleModels = maps.Clone(models)
}

// Spawn starts a job asynchronously. The returned Info reflects starting state.
//
// Concurrency: if MaxConcurrent slots are full, Spawn returns [ErrBusy]
// (jobs are not queued).
func (m *Manager) Spawn(ctx context.Context, req SpawnRequest) (Info, error) {
	if err := req.validate(); err != nil {
		return Info{}, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Info{}, ErrClosed
	}
	select {
	case m.slots <- struct{}{}:
	default:
		m.mu.Unlock()
		return Info{}, ErrBusy
	}
	model := m.roleModels[NormalizeRole(string(req.Role))]
	m.mu.Unlock()

	id, err := newJobID()
	if err != nil {
		<-m.slots
		return Info{}, err
	}
	now := time.Now().UTC()
	meta := Meta{
		ID:          id,
		ParentID:    req.ParentID,
		Role:        NormalizeRole(string(req.Role)),
		Prompt:      req.Prompt,
		Description: req.Description,
		WorkDir:     req.WorkDir,
		Status:      StatusStarting,
		CreatedAt:   now,
	}
	meta, err = m.store.Create(meta)
	if err != nil {
		<-m.slots
		return Info{}, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, req.Timeout)
	}
	// Parent ctx cancel does not kill the job (jobs outlive a single tool call);
	// callers use Cancel. Still respect if Spawn itself is aborted before start.
	if err := ctx.Err(); err != nil {
		cancel()
		<-m.slots
		meta.Status = StatusCancelled
		meta.FinishedAt = time.Now().UTC()
		meta.Error = err.Error()
		m.persistMeta(meta)
		return Info{}, err
	}

	lj := &liveJob{
		meta:            meta,
		parentToolUseID: req.ParentToolUseID,
		parentContext:   req.ParentContext,
		model:           model,
		cancel:          cancel,
		done:            make(chan struct{}),
	}
	m.mu.Lock()
	m.jobs[id] = lj
	m.mu.Unlock()

	go m.run(runCtx, lj)
	return Info{Meta: meta}, nil
}

func (m *Manager) run(ctx context.Context, lj *liveJob) {
	defer close(lj.done)
	defer func() { <-m.slots }()

	meta := lj.meta
	meta.Status = StatusRunning
	meta.StartedAt = time.Now().UTC()
	m.persistMeta(meta)
	m.setLiveMeta(meta)
	m.persistEvent(meta, "running")

	env := RunEnv{
		Job:           meta,
		ParentContext: lj.parentContext,
		Model:         lj.model,
		Log: func(message string) {
			m.persistEvent(meta, message)
		},
		WriteResult: func(summary string) error {
			err := m.store.WriteResult(meta.ID, summary)
			if err != nil {
				m.reportStore("writeResult", meta.ID, err)
			}
			return err
		},
		OnProgress: func(p Progress) {
			p.JobID = meta.ID
			if p.ParentToolUseID == "" {
				p.ParentToolUseID = lj.parentToolUseID
			}
			if p.Time.IsZero() {
				p.Time = time.Now().UTC()
			}
			m.emitProgress(p)
		},
	}

	summary, err := m.runner.Run(ctx, env)

	meta.FinishedAt = time.Now().UTC()

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		meta.Status = StatusTimedOut
		if err != nil {
			meta.Error = err.Error()
		} else {
			meta.Error = ctx.Err().Error()
		}
		m.persistEvent(meta, "timed_out: "+meta.Error)
	case errors.Is(ctx.Err(), context.Canceled):
		meta.Status = StatusCancelled
		if err != nil {
			meta.Error = err.Error()
		} else {
			meta.Error = ctx.Err().Error()
		}
		m.persistEvent(meta, "cancelled: "+meta.Error)
	case err != nil:
		meta.Status = StatusFailed
		meta.Error = err.Error()
		m.persistEvent(meta, "failed: "+meta.Error)
	default:
		meta.Status = StatusCompleted
		if summary != "" {
			if werr := m.store.WriteResult(meta.ID, summary); werr != nil {
				m.reportStore("writeResult", meta.ID, werr)
				meta.Status = StatusFailed
				meta.Error = "failed to write result.md: " + werr.Error()
				m.persistEvent(meta, "failed: "+meta.Error)
			} else {
				m.persistEvent(meta, "completed")
			}
		} else {
			m.persistEvent(meta, "completed")
		}
	}

	m.persistMeta(meta)
	m.setLiveMeta(meta)

	m.mu.Lock()
	delete(m.jobs, meta.ID)
	m.mu.Unlock()
}

func (m *Manager) setLiveMeta(meta Meta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lj, ok := m.jobs[meta.ID]; ok {
		lj.meta = meta
	}
}

// Get returns one job by id.
func (m *Manager) Get(ctx context.Context, id string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	return m.loadInfo(id)
}

func (m *Manager) loadInfo(id string) (Info, error) {
	m.mu.Lock()
	if lj, ok := m.jobs[id]; ok {
		meta := lj.meta
		m.mu.Unlock()
		return Info{Meta: meta}, nil
	}
	m.mu.Unlock()
	meta, err := m.store.Load(id)
	if err != nil {
		return Info{}, err
	}
	return Info{Meta: meta}, nil
}

// Wait blocks until the job reaches a terminal status or ctx is done.
//
// A Wait timeout (ctx deadline) does not cancel the job; use [Manager.Cancel].
func (m *Manager) Wait(ctx context.Context, id string) (WaitResult, error) {
	for {
		info, err := m.loadInfo(id)
		if err != nil {
			return WaitResult{}, err
		}
		if info.Status.Terminal() {
			summary, _ := m.store.ReadResult(info.ID)
			return WaitResult{Info: info, Summary: summary}, nil
		}

		m.mu.Lock()
		lj, ok := m.jobs[id]
		var done <-chan struct{}
		if ok {
			done = lj.done
		}
		m.mu.Unlock()

		if !ok {
			info, err = m.loadInfo(id)
			if err != nil {
				return WaitResult{}, err
			}
			if info.Status.Terminal() {
				summary, _ := m.store.ReadResult(info.ID)
				return WaitResult{Info: info, Summary: summary}, nil
			}
			return WaitResult{}, fmt.Errorf("%w: %s disappeared while non-terminal", ErrNotFound, id)
		}

		select {
		case <-ctx.Done():
			return WaitResult{}, fmt.Errorf("%w: %w", ErrWaitTimeout, ctx.Err())
		case <-done:
		}
	}
}

// Log returns the last limit events (0 = all).
func (m *Manager) Log(ctx context.Context, id string, limit int) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := m.loadInfo(id)
	if err != nil {
		return nil, err
	}
	return m.store.ReadEvents(info.ID, limit)
}

// Cancel requests cancellation of a running/starting job.
func (m *Manager) Cancel(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	lj, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		info, err := m.loadInfo(id)
		if err != nil {
			return err
		}
		if info.Status.Terminal() {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrNotRunning, id)
	}
	lj.cancel()
	select {
	case <-lj.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close cancels all live jobs and waits for them to exit.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	lives := make([]*liveJob, 0, len(m.jobs))
	for _, lj := range m.jobs {
		lives = append(lives, lj)
		lj.cancel()
	}
	// Close subscriber channels under the lock so they cannot race a send in
	// emitProgress (which also holds m.mu). The closed flag makes Close safe
	// even if a subscriber's cancel func runs later.
	for _, s := range m.subs {
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
	}
	m.subs = nil
	m.mu.Unlock()

	for _, lj := range lives {
		select {
		case <-lj.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Subscribe receives live [Progress] events. The channel is buffered; slow
// consumers may miss events. Cancel closes the channel and unregisters.
func (m *Manager) Subscribe() (<-chan Progress, func()) {
	ch := make(chan Progress, m.progressBufferSize)
	sub := &subscriber{ch: ch}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	m.subs = append(m.subs, sub)
	m.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			for i, s := range m.subs {
				if s == sub {
					m.subs = slices.Delete(m.subs, i, i+1)
					break
				}
			}
			// Idempotent vs Manager.Close: never close an already closed channel.
			if !sub.closed {
				sub.closed = true
				close(ch)
			}
		})
	}
	return ch, cancel
}

func (m *Manager) emitProgress(p Progress) {
	// Sends are non-blocking, so holding the lock is safe; it makes send and
	// channel close mutually exclusive (cancel/Close close under m.mu).
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.subs {
		select {
		case s.ch <- p:
		default:
			// drop if subscriber is slow
		}
	}
}

// ParentToolUseID returns the UI correlation id for a live job, if any.
func (m *Manager) ParentToolUseID(jobID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lj, ok := m.jobs[jobID]; ok {
		return lj.parentToolUseID
	}
	return ""
}

// MaxConcurrent exposes the concurrency ceiling.
func (m *Manager) MaxConcurrent() int { return m.maxConcurrent }

// LiveCount returns how many jobs are currently starting/running in-process.
func (m *Manager) LiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.jobs)
}
