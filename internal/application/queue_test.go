package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func queueTestService(paused bool) *Service {
	return &Service{queuePaused: paused}
}

func queueTestPlan(profile config.Profile, items ...BatchItem) *transferPlan {
	planned := make([]batchPlannedItem, len(items))
	for i, item := range items {
		planned[i] = batchPlannedItem{item: item}
	}
	return &transferPlan{
		profile: profile,
		batch: &batchPlan{
			preview: BatchPreview{Token: "token", Direction: "upload", Server: "origin"},
			items:   planned,
		},
	}
}

func enqueueQueueTestBatch(t *testing.T, service *Service, profile config.Profile, items ...BatchItem) QueueState {
	t.Helper()
	service.mu.Lock()
	service.pending = queueTestPlan(profile, items...)
	service.mu.Unlock()
	state, err := service.EnqueueBatch(context.Background(), "token", "skip", true)
	if err != nil {
		t.Fatalf("EnqueueBatch() error = %v", err)
	}
	return state
}

func waitQueueDone(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		done := service.queueWorker == nil
		service.mu.Unlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("queue worker did not finish")
}

func TestEnqueueBatchConsumesTokenOnceAndValidationPreservesIt(t *testing.T) {
	service := queueTestService(true)
	profile := config.Profile{Name: "sample", LocalRoot: t.TempDir(), RemoteRoot: "/remote"}
	service.pending = queueTestPlan(profile,
		BatchItem{RelativePath: ".env.local", Kind: "file", Size: 10},
	)
	if _, err := service.EnqueueBatch(context.Background(), "token", "skip", false); err == nil {
		t.Fatal("EnqueueBatch() accepted an environment file without confirmation")
	}
	if service.pending == nil {
		t.Fatal("environment validation consumed the preview token")
	}
	if _, err := service.EnqueueBatch(context.Background(), "token", "invalid", true); err == nil {
		t.Fatal("EnqueueBatch() accepted an unknown conflict policy")
	}
	if service.pending == nil {
		t.Fatal("policy validation consumed the preview token")
	}
	if _, err := service.EnqueueBatch(context.Background(), "stale", "skip", true); err == nil {
		t.Fatal("EnqueueBatch() accepted a stale token")
	}
	if service.pending == nil {
		t.Fatal("stale token attempt consumed the preview token")
	}
	if _, err := service.EnqueueBatch(context.Background(), "token", "skip", true); err != nil {
		t.Fatalf("EnqueueBatch() with confirmation error = %v", err)
	}
	if service.pending != nil {
		t.Fatal("successful enqueue did not consume the preview token")
	}
	if _, err := service.EnqueueBatch(context.Background(), "token", "skip", true); err == nil {
		t.Fatal("EnqueueBatch() consumed the same preview token twice")
	}
}

func TestEnqueueBatchSnapshotsQueueItemsAndOriginalSettings(t *testing.T) {
	service := queueTestService(true)
	root := t.TempDir()
	profile := config.Profile{Name: "original", LocalRoot: root, RemoteRoot: "/original", Excludes: []string{"vendor"}, Connection: transport.Connection{Mode: "manual", Host: "host-a", User: "alice", Port: 22}}
	service.pending = queueTestPlan(profile, BatchItem{RelativePath: "a.txt", Source: filepath.Join(root, "a.txt"), Destination: "/original/a.txt", Kind: "file", Size: 4})
	state, err := service.EnqueueBatch(context.Background(), "token", "skip", true)
	if err != nil {
		t.Fatal(err)
	}
	jobID := state.Jobs[0].ID
	state.Jobs[0].Items[0].RelativePath = "mutated-return-value"
	state.Jobs[0].Items[0].Size = 900
	got := service.GetQueue()
	if got.Jobs[0].Items[0].RelativePath != "a.txt" || got.Jobs[0].Items[0].Size != 4 {
		t.Fatalf("returned queue snapshot changed stored item: %+v", got.Jobs[0].Items[0])
	}
	got.Jobs[0].Items[0].Destination = "/mutated"
	if again := service.GetQueue(); again.Jobs[0].Items[0].Destination != "/original/a.txt" {
		t.Fatalf("GetQueue() item aliases internal state: %+v", again.Jobs[0].Items[0])
	}

	// Later edits to the selected settings must not redirect an accepted job.
	service.status.Settings.Profile = &config.Profile{Name: "changed", LocalRoot: "C:/changed", RemoteRoot: "/changed", Connection: transport.Connection{Host: "host-b", User: "bob"}}
	service.status.Settings.ProjectName = "changed"
	service.mu.Lock()
	entry := service.queue[0]
	service.mu.Unlock()
	if entry.job.ID != jobID || entry.job.ProjectName != "original" || entry.profile.LocalRoot != root || entry.profile.RemoteRoot != "/original" || entry.profile.Connection.Host != "host-a" || entry.plan.items[0].item.Destination != "/original/a.txt" || entry.profile.Excludes[0] != "vendor" {
		t.Fatalf("queued snapshot followed changed settings: job=%+v profile=%+v plan=%+v", entry.job, entry.profile, entry.plan.items[0])
	}
}

func TestEnqueueBatchTotalBytesExcludeSkippedFiles(t *testing.T) {
	service := queueTestService(true)
	state := enqueueQueueTestBatch(t, service, config.Profile{Name: "sample"},
		BatchItem{RelativePath: "new.txt", Kind: "file", Size: 12},
		BatchItem{RelativePath: "existing.txt", Kind: "file", Size: 20, Exists: true},
		BatchItem{RelativePath: "excluded.txt", Kind: "file", Size: 40, SkippedReason: "excluded"},
		BatchItem{RelativePath: "dir", Kind: "directory", Size: 100},
	)
	if got := state.Jobs[0].TotalBytes; got != 12 {
		t.Fatalf("TotalBytes = %d, want 12", got)
	}
}

func TestCancelWaitingJobAndClearFinishedRetainsWaiting(t *testing.T) {
	service := queueTestService(true)
	first := enqueueQueueTestBatch(t, service, config.Profile{Name: "first"}, BatchItem{RelativePath: "a", Kind: "file", Size: 1})
	second := enqueueQueueTestBatch(t, service, config.Profile{Name: "second"}, BatchItem{RelativePath: "b", Kind: "file", Size: 1})
	secondID := second.Jobs[len(second.Jobs)-1].ID
	if err := service.CancelQueueJob(first.Jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	service.ClearFinishedQueue()
	state := service.GetQueue()
	if len(state.Jobs) != 1 || state.Jobs[0].ID != secondID || state.Jobs[0].Status != "waiting" {
		t.Fatalf("ClearFinishedQueue() state = %+v, want only the waiting job", state)
	}
	service.mu.Lock()
	ssh := service.status.SSHPath
	service.mu.Unlock()
	if ssh != "" {
		t.Fatalf("test service unexpectedly has SSH executable %q", ssh)
	}
}

func TestQueueLimitIsFiftyAndDoesNotConsumeRejectedToken(t *testing.T) {
	service := queueTestService(true)
	profile := config.Profile{Name: "sample"}
	for i := 0; i < maxQueueJobs; i++ {
		service.mu.Lock()
		service.pending = queueTestPlan(profile, BatchItem{RelativePath: "file", Kind: "file", Size: 1})
		service.mu.Unlock()
		if _, err := service.EnqueueBatch(context.Background(), "token", "skip", true); err != nil {
			t.Fatalf("EnqueueBatch() #%d error = %v", i+1, err)
		}
	}
	service.mu.Lock()
	service.pending = queueTestPlan(profile, BatchItem{RelativePath: "extra", Kind: "file", Size: 1})
	service.mu.Unlock()
	if _, err := service.EnqueueBatch(context.Background(), "token", "skip", true); err == nil {
		t.Fatal("EnqueueBatch() exceeded the 50 job limit")
	}
	if service.pending == nil || len(service.GetQueue().Jobs) != maxQueueJobs {
		t.Fatal("queue limit rejection consumed the pending token or changed the queue")
	}
}

func TestQueueWaitingLockCancellationAndShutdownDoNotOpenSSH(t *testing.T) {
	t.Run("targeted cancel", func(t *testing.T) {
		service := queueTestService(false)
		service.status.SSHPath = filepath.Join(t.TempDir(), "missing-ssh")
		service.operationMu.Lock()
		state := enqueueQueueTestBatch(t, service, config.Profile{Name: "cancel"}, BatchItem{RelativePath: "a", Kind: "file", Size: 1})
		waitForQueueStatus(t, service, state.Jobs[0].ID, "running")
		if err := service.CancelQueueJob(state.Jobs[0].ID); err != nil {
			t.Fatal(err)
		}
		service.operationMu.Unlock()
		waitQueueDone(t, service)
		if got := service.GetQueue().Jobs[0].Status; got != "cancelled" {
			t.Fatalf("job status = %q, want cancelled", got)
		}
	})

	t.Run("shutdown", func(t *testing.T) {
		service := queueTestService(false)
		service.status.SSHPath = filepath.Join(t.TempDir(), "missing-ssh")
		service.operationMu.Lock()
		state := enqueueQueueTestBatch(t, service, config.Profile{Name: "shutdown"}, BatchItem{RelativePath: "a", Kind: "file", Size: 1})
		waitForQueueStatus(t, service, state.Jobs[0].ID, "running")
		shutdownDone := make(chan struct{})
		go func() { service.Shutdown(); close(shutdownDone) }()
		service.operationMu.Unlock()
		select {
		case <-shutdownDone:
		case <-time.After(5 * time.Second):
			t.Fatal("Shutdown() did not wait for and reap the queued worker")
		}
		if got := service.GetQueue().Jobs[0].Status; got != "cancelled" {
			t.Fatalf("job status = %q, want cancelled", got)
		}
	})
}

func waitForQueueStatus(t *testing.T, service *Service, id, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range service.GetQueue().Jobs {
			if job.ID == id && job.Status == status {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %q did not reach status %q: %+v", id, status, service.GetQueue())
}

func TestQueueConcurrentSnapshotsPauseAndCancel(t *testing.T) {
	service := queueTestService(true)
	state := enqueueQueueTestBatch(t, service, config.Profile{Name: "parallel"}, BatchItem{RelativePath: "a", Kind: "file", Size: 1})
	service.operationMu.Lock()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				_ = service.GetQueue()
				service.SetQueuePaused(index%2 == 0)
				if index == 0 && n == 0 {
					_ = service.CancelQueueJob(state.Jobs[0].ID)
				}
			}
		}(i)
	}
	wg.Wait()
	service.operationMu.Unlock()
	waitQueueDone(t, service)
	service.SetQueuePaused(true)
	if got := service.GetQueue().Jobs[0].Status; got != "cancelled" {
		t.Fatalf("job status after concurrent queue operations = %q, want cancelled", got)
	}
}

func TestEnqueueBatchContextErrorPreservesToken(t *testing.T) {
	service := queueTestService(true)
	service.pending = queueTestPlan(config.Profile{Name: "sample"}, BatchItem{RelativePath: "a", Kind: "file", Size: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.EnqueueBatch(ctx, "token", "skip", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnqueueBatch() error = %v, want context.Canceled", err)
	}
	if service.pending == nil {
		t.Fatal("cancelled enqueue consumed the preview token")
	}
}
