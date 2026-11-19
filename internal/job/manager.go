package job

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
)

type options struct {
	maxConcurrent int
	maxDepth      int
	recovery      RecoveryMode
	parentID      string
	workDir       string
	parentDepth   int
	roleModels    map[Role]llm.ModelConfig
	onStoreError  func(op, jobID string, err error)
}

// Option configures optional job manager behavior.
type Option func(*options)

// WithParent sets the initial parent session, working directory, and nesting depth.
func WithParent(id, workDir string, depth int) Option {
	return func(o *options) { o.parentID, o.workDir, o.parentDepth = id, workDir, depth }
}

// WithRoleModels supplies the initial models for child jobs. New copies the map.
func WithRoleModels(models map[Role]llm.ModelConfig) Option {
	return func(o *options) { o.roleModels = maps.Clone(models) }
}

// WithMaxConcurrent limits the number of running jobs. Non-positive values use the default of 4.
func WithMaxConcurrent(n int) Option {
	return func(o *options) { o.maxConcurrent = n }
}

// WithMaxDepth limits nesting. Non-positive values use the default of 1.
func WithMaxDepth(n int) Option {
	return func(o *options) { o.maxDepth = n }
}

// WithRecovery selects how to handle unfinished jobs from a previous process.
func WithRecovery(mode RecoveryMode) Option {
	return func(o *options) { o.recovery = mode }
}

// WithOnStoreError reports disk write failures after a job is live.
func WithOnStoreError(fn func(op, jobID string, err error)) Option {
	return func(o *options) { o.onStoreError = fn }
}

// Manager owns in-process job lifecycles and their persistence.
type Manager struct {
	store         store
	runner        Runner
	maxConcurrent int
	maxDepth      int
	onStoreError  func(op, jobID string, err error)

	mu          sync.Mutex
	closed      bool
	slots       chan struct{}
	jobs        map[string]*liveJob
	subs        []*subscriber
	roleModels  map[Role]llm.ModelConfig
	parentID    string
	workDir     string
	parentDepth int
}

type liveJob struct {
	meta            Meta
	parentToolUseID string
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
	st, err := newDiskStore(root)
	if err != nil {
		return nil, err
	}
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}
	maxC := cfg.maxConcurrent
	if maxC <= 0 {
		maxC = 4
	}
	maxD := cfg.maxDepth
	if maxD <= 0 {
		maxD = 1
	}
	m := &Manager{
		store:         st,
		runner:        runner,
		maxConcurrent: maxC,
		maxDepth:      maxD,
		onStoreError:  cfg.onStoreError,
		parentID:      cfg.parentID,
		workDir:       cfg.workDir,
		parentDepth:   cfg.parentDepth,
		roleModels:    cfg.roleModels,
		slots:         make(chan struct{}, maxC),
		jobs:          make(map[string]*liveJob),
	}
	if cfg.recovery != RecoverIgnore {
		if err := m.recoverStale(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) recoverStale() error {
	ids, err := m.store.listIDs()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, id := range ids {
		meta, err := m.store.readMeta(id)
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
		if err := m.store.writeMeta(meta); err != nil {
			return fmt.Errorf("job: recover %s: %w", id, err)
		}
		if err := m.store.appendEvent(meta, "recovered: marked failed after restart"); err != nil {
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
	if err := m.store.writeMeta(meta); err != nil {
		m.reportStore("writeMeta", meta.ID, err)
	}
}

func (m *Manager) persistEvent(meta Meta, msg string) {
	if err := m.store.appendEvent(meta, msg); err != nil {
		m.reportStore("appendEvent", meta.ID, err)
	}
}

// SetRoleModels replaces the models for future jobs. The map is copied;
// callers and runners must treat slices and maps within each config as immutable.
func (m *Manager) SetRoleModels(models map[Role]llm.ModelConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roleModels = maps.Clone(models)
}

// SetParent sets the session, default working directory and depth for future spawns.
func (m *Manager) SetParent(id, workDir string, depth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parentID, m.workDir, m.parentDepth = id, workDir, depth
}

// Spawn starts a job asynchronously. The returned Info reflects starting state.
//
// Concurrency: if MaxConcurrent slots are full, Spawn returns [ErrBusy]
// (jobs are not queued).
func (m *Manager) Spawn(ctx context.Context, req SpawnRequest) (Info, error) {
	if err := req.validate(); err != nil {
		return Info{}, err
	}

	role := NormalizeRole(string(req.Role))
	m.mu.Lock()
	parentID, workDir, depth := m.parentID, m.workDir, m.parentDepth
	if depth < 0 {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("%w: depth %d is negative", ErrInvalid, depth)
	}
	if depth >= m.maxDepth {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("%w: depth %d >= max %d", ErrDepth, depth, m.maxDepth)
	}
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
	model := m.roleModels[role]
	m.mu.Unlock()

	id, err := newJobID()
	if err != nil {
		<-m.slots
		return Info{}, err
	}
	now := time.Now().UTC()
	if req.WorkDir != "" {
		workDir = req.WorkDir
	}
	meta := Meta{
		ID:          id,
		ParentID:    parentID,
		ParentDepth: depth,
		Role:        role,
		Prompt:      req.Prompt,
		Description: req.Description,
		WorkDir:     workDir,
		Status:      StatusStarting,
		CreatedAt:   now,
	}
	meta, err = m.store.create(meta)
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
		cancel:          cancel,
		done:            make(chan struct{}),
	}
	m.mu.Lock()
	m.jobs[id] = lj
	m.mu.Unlock()

	go m.run(runCtx, lj, model)
	return Info{Meta: meta}, nil
}

func (m *Manager) run(ctx context.Context, lj *liveJob, model llm.ModelConfig) {
	defer close(lj.done)
	defer func() { <-m.slots }()

	meta := lj.meta
	meta.Status = StatusRunning
	meta.StartedAt = time.Now().UTC()
	m.persistMeta(meta)
	m.setLiveMeta(meta)
	m.persistEvent(meta, "running")

	env := RunEnv{
		Job:   meta,
		Model: model,
		Log: func(message string) {
			m.persistEvent(meta, message)
		},
		WriteResult: func(summary string) error {
			err := m.store.writeResult(meta, summary)
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
			if werr := m.store.writeResult(meta, summary); werr != nil {
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
	meta, err := m.store.readMeta(id)
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
			summary, _ := m.store.readResult(info.Meta)
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
				summary, _ := m.store.readResult(info.Meta)
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
	return m.store.readEvents(info.Meta, limit)
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
	ch := make(chan Progress, 64)
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

// MaxDepth exposes the configured nesting ceiling (for tool adapters).
func (m *Manager) MaxDepth() int { return m.maxDepth }

// MaxConcurrent exposes the concurrency ceiling.
func (m *Manager) MaxConcurrent() int { return m.maxConcurrent }

// LiveCount returns how many jobs are currently starting/running in-process.
func (m *Manager) LiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.jobs)
}
