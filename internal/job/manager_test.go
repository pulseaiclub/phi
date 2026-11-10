package job_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
)

func newMgr(t *testing.T, runner job.Runner, opts job.Options) *job.Manager {
	t.Helper()
	if opts.Root == "" {
		opts.Root = t.TempDir()
	}
	opts.Runner = runner
	m, err := job.New(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		// t.Context is canceled before cleanup; Close must still wait for job writes.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, m.Close(ctx))
	})
	return m
}

func TestSpawnWaitResultOnDisk(t *testing.T) {
	m := newMgr(t, job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
		env.Log("step-1")
		return "hello from " + env.Job.ID, nil
	}), job.Options{})

	ctx := t.Context()
	a, err := m.Spawn(ctx, job.SpawnRequest{Prompt: "explore A", Description: "A"})
	require.NoError(t, err)
	b, err := m.Spawn(ctx, job.SpawnRequest{Prompt: "explore B", Description: "B"})
	require.NoError(t, err)

	ra, err := m.Wait(ctx, a.ID)
	require.NoError(t, err)
	rb, err := m.Wait(ctx, b.ID)
	require.NoError(t, err)

	assert.Equal(t, job.StatusCompleted, ra.Info.Status)
	assert.Equal(t, job.StatusCompleted, rb.Info.Status)
	assert.Contains(t, ra.Summary, a.ID)
	assert.Contains(t, rb.Summary, b.ID)

	data, err := os.ReadFile(ra.Info.ResultPath)
	require.NoError(t, err)
	assert.Equal(t, ra.Summary, string(data))

	events, err := m.Log(ctx, a.ID, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 2)
	assert.Equal(t, "step-1", events[1].Message)
}

func TestCancelStopsRunner(t *testing.T) {
	started := make(chan struct{})
	m := newMgr(t, job.RunnerFunc(func(ctx context.Context, _ job.RunEnv) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}), job.Options{})

	ctx := t.Context()
	info, err := m.Spawn(ctx, job.SpawnRequest{Prompt: "long"})
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		require.Fail(t, "runner did not start")
	}

	require.NoError(t, m.Cancel(ctx, info.ID))
	res, err := m.Wait(ctx, info.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusCancelled, res.Info.Status)
}

func TestSpawnTimeoutIsTimedOut(t *testing.T) {
	m := newMgr(t, job.RunnerFunc(func(ctx context.Context, _ job.RunEnv) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}), job.Options{})

	info, err := m.Spawn(t.Context(), job.SpawnRequest{
		Prompt:  "slow",
		Timeout: 50 * time.Millisecond,
	})
	require.NoError(t, err)

	res, err := m.Wait(t.Context(), info.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusTimedOut, res.Info.Status)
}

func TestDepthLimit(t *testing.T) {
	m := newMgr(t, job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "ok", nil
	}), job.Options{MaxDepth: 1})

	_, err := m.Spawn(t.Context(), job.SpawnRequest{Prompt: "nested", Depth: 1})
	require.ErrorIs(t, err, job.ErrDepth)
}

func TestConcurrencyBusy(t *testing.T) {
	block := make(chan struct{})
	m := newMgr(t, job.RunnerFunc(func(ctx context.Context, _ job.RunEnv) (string, error) {
		select {
		case <-block:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}), job.Options{MaxConcurrent: 1})

	ctx := t.Context()
	info, err := m.Spawn(ctx, job.SpawnRequest{Prompt: "1"})
	require.NoError(t, err)
	_, err = m.Spawn(ctx, job.SpawnRequest{Prompt: "2"})
	require.ErrorIs(t, err, job.ErrBusy)
	close(block)
	_, err = m.Wait(ctx, info.ID)
	require.NoError(t, err)
}

func TestHandleWaitTimeoutDoesNotCancelJob(t *testing.T) {
	m := newMgr(t, job.RunnerFunc(func(ctx context.Context, _ job.RunEnv) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}), job.Options{})

	info, err := m.Spawn(t.Context(), job.SpawnRequest{Prompt: "slow"})
	require.NoError(t, err)

	raw, _ := json.Marshal(job.WaitArgs{JobID: info.ID, TimeoutSec: 1})
	result, err := m.HandleWait(t.Context(), raw)
	require.NoError(t, err)
	assert.Equal(t, info.ID, result.Info.ID)
	assert.False(t, result.Info.Status.Terminal())

	// Job still live until Cancel.
	got, err := m.Get(t.Context(), info.ID)
	require.NoError(t, err)
	assert.False(t, got.Status.Terminal())

	require.NoError(t, m.Cancel(t.Context(), info.ID))
}

func TestMetaPersisted(t *testing.T) {
	root := t.TempDir()
	m := newMgr(t, job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "summary", nil
	}), job.Options{Root: root})

	info, err := m.Spawn(t.Context(), job.SpawnRequest{
		Prompt:   "p",
		ParentID: "sess-1",
		WorkDir:  "/tmp/ws",
	})
	require.NoError(t, err)
	_, err = m.Wait(t.Context(), info.ID)
	require.NoError(t, err)

	metaPath := filepath.Join(root, info.ID, "meta.json")
	data, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"parent_id": "sess-1"`)
	assert.Contains(t, string(data), `"status": "completed"`)
}

func TestRecoverStaleJobsOnNew(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "job_zombie")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	// strconv.Quote so Windows paths with backslashes remain valid JSON.
	meta := `{
  "id": "job_zombie",
  "prompt": "left over",
  "status": "running",
  "created_at": "2026-01-01T00:00:00Z",
  "dir": ` + strconv.Quote(dir) + `,
  "result_path": ` + strconv.Quote(filepath.Join(dir, "result.md")) + `,
  "events_path": ` + strconv.Quote(filepath.Join(dir, "events.jsonl")) + `
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), nil, 0o644))

	m := newMgr(t, job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "ok", nil
	}), job.Options{Root: root})

	info, err := m.Get(t.Context(), "job_zombie")
	require.NoError(t, err)
	assert.Equal(t, job.StatusFailed, info.Status)
	assert.Contains(t, info.Error, "interrupted")

	res, err := m.Wait(t.Context(), "job_zombie")
	require.NoError(t, err)
	assert.Equal(t, job.StatusFailed, res.Info.Status)
}

func TestRecoverIgnoreLeavesStale(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "job_zombie")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	meta := `{
  "id": "job_zombie",
  "prompt": "left over",
  "status": "running",
  "created_at": "2026-01-01T00:00:00Z",
  "dir": ` + strconv.Quote(dir) + `
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644))

	m, err := job.New(job.Options{
		Root:     root,
		Recovery: job.RecoverIgnore,
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close(t.Context()) })

	info, err := m.Get(t.Context(), "job_zombie")
	require.NoError(t, err)
	assert.Equal(t, job.StatusRunning, info.Status)
}

func TestOnStoreErrorCallback(t *testing.T) {
	var (
		mu  sync.Mutex
		ops []string
	)
	// Use a root that becomes unusable for append by pointing EventsPath wrong
	// is hard; instead verify callback wiring via a runner that WriteResult
	// fails when we force a bad path — simpler: mark zombie recover with bad dir.
	// Direct unit: call Persist through a completed job with OnStoreError set,
	// then chmod meta dir read-only after start... platform dependent.
	//
	// Smoke: ensure option is accepted and normal path never fires.
	m := newMgr(t, job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "ok", nil
	}), job.Options{
		OnStoreError: func(op, _ string, _ error) {
			mu.Lock()
			ops = append(ops, op)
			mu.Unlock()
		},
	})
	info, err := m.Spawn(t.Context(), job.SpawnRequest{Prompt: "x"})
	require.NoError(t, err)
	_, err = m.Wait(t.Context(), info.ID)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, ops)
}

func TestSubscribeProgress(t *testing.T) {
	m := newMgr(t, job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
		env.OnProgress(job.Progress{
			ToolUseID: "child-1",
			Name:      "read",
			Status:    "in-progress",
			Detail:    "foo.go",
		})
		env.OnProgress(job.Progress{
			ToolUseID: "child-1",
			Name:      "read",
			Status:    "done",
			Detail:    "foo.go",
		})
		return "done", nil
	}), job.Options{})

	ch, cancel := m.Subscribe()
	defer cancel()

	info, err := m.Spawn(t.Context(), job.SpawnRequest{
		Prompt:          "p",
		ParentToolUseID: "parent-tool-1",
	})
	require.NoError(t, err)

	var got []job.Progress
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case p, ok := <-ch:
			require.True(t, ok)
			got = append(got, p)
		case <-deadline:
			require.Failf(t, "timeout", "got %d events", len(got))
		}
	}
	assert.Equal(t, info.ID, got[0].JobID)
	assert.Equal(t, "parent-tool-1", got[0].ParentToolUseID)
	assert.Equal(t, "read", got[0].Name)
	assert.Equal(t, "in-progress", got[0].Status)
	assert.Equal(t, "done", got[1].Status)

	_, err = m.Wait(t.Context(), info.ID)
	require.NoError(t, err)
}

func TestSubscribeCancelAfterClose(t *testing.T) {
	m := newMgr(t, job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "ok", nil
	}), job.Options{})

	_, cancel := m.Subscribe()
	require.NoError(t, m.Close(t.Context()))
	// Must be a no-op: Manager.Close already closed the subscriber channel.
	cancel()
}

// TestEmitProgressCancelRace is a regression test for a send-on-closed-channel
// panic: emitProgress used to clone the subscriber list under the lock and send
// outside it, racing with cancel/Close which close the channel. With the fix,
// sends and closes are mutually exclusive under m.mu. Without it, this test
// crashes the test binary with "panic: send on closed channel".
func TestEmitProgressCancelRace(t *testing.T) {
	for range 200 {
		m := newMgr(t, job.RunnerFunc(func(ctx context.Context, env job.RunEnv) (string, error) {
			for ctx.Err() == nil {
				env.OnProgress(job.Progress{Name: "read", Status: "in-progress", Detail: "x"})
			}
			return "", ctx.Err()
		}), job.Options{})

		ch, cancel := m.Subscribe()
		go func() {
			for range ch {
				_ = ch
			}
		}()
		_, err := m.Spawn(t.Context(), job.SpawnRequest{Prompt: "p"})
		require.NoError(t, err)
		time.Sleep(500 * time.Microsecond) // let the runner start emitting
		cancel()                           // removes + closes the channel
		require.NoError(t, m.Close(t.Context()))
	}
}
