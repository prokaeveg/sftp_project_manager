package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"time"
)

const maxQueueJobs = 50

type QueueItem struct {
	BatchItem
	Status           string `json:"status"`
	TransferredBytes int64  `json:"transferredBytes"`
	Error            string `json:"error"`
}

type QueueJob struct {
	ID               string      `json:"id"`
	ProjectName      string      `json:"projectName"`
	Direction        string      `json:"direction"`
	Server           string      `json:"server"`
	Status           string      `json:"status"`
	Items            []QueueItem `json:"items"`
	TotalBytes       int64       `json:"totalBytes"`
	TransferredBytes int64       `json:"transferredBytes"`
	ProcessedItems   int         `json:"processedItems"`
	Error            string      `json:"error"`
}

type QueueState struct {
	Paused bool       `json:"paused"`
	Jobs   []QueueJob `json:"jobs"`
}

type queueEntry struct {
	job     QueueJob
	profile config.Profile
	plan    *batchPlan
	policy  string
	ctx     context.Context
	cancel  context.CancelFunc
}

type queueWaitKey struct{}
type batchObserverKey struct{}
type batchObserver struct {
	start    func(int)
	progress func(int, int64)
	finish   func(int, BatchItemResult)
}

func validateBatchConfirmation(plan *batchPlan, policy string, includeEnvironment bool) error {
	if policy != "skip" && policy != "overwrite" {
		return errors.New("выберите политику skip или overwrite")
	}
	if !includeEnvironment {
		for _, planned := range plan.items {
			name := strings.ToLower(path.Base(planned.item.RelativePath))
			if planned.item.Kind == "file" && planned.item.SkippedReason == "" && (name == ".env" || strings.HasPrefix(name, ".env.")) {
				return errors.New("для передачи .env требуется явное подтверждение")
			}
		}
	}
	return nil
}

// EnqueueBatch consumes the preview once and retains its private snapshot.
// Settings and the selected project never determine a queued destination.
func (s *Service) EnqueueBatch(ctx context.Context, token, policy string, includeEnvironment bool) (QueueState, error) {
	if policy == "" {
		policy = "skip"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queueClosed {
		return QueueState{}, errors.New("приложение закрывается")
	}
	if err := ctx.Err(); err != nil {
		return QueueState{}, err
	}
	if len(s.queue) >= maxQueueJobs {
		return QueueState{}, errors.New("очередь заполнена: очистите завершённые задания (лимит 50)")
	}
	if s.pending == nil || s.pending.batch == nil || s.pending.batch.preview.Token != token {
		return QueueState{}, errors.New("предпросмотр устарел; повторите его")
	}
	if err := validateBatchConfirmation(s.pending.batch, policy, includeEnvironment); err != nil {
		return QueueState{}, err
	}
	id, err := newID()
	if err != nil {
		return QueueState{}, err
	}
	plan := s.pending.batch
	job := QueueJob{ID: id, ProjectName: s.pending.profile.Name, Direction: plan.preview.Direction, Server: plan.preview.Server, Status: "waiting", Items: make([]QueueItem, len(plan.items))}
	for index, item := range plan.items {
		job.Items[index] = QueueItem{BatchItem: item.item, Status: "waiting"}
		if item.item.Kind == "file" && item.item.SkippedReason == "" && !(policy == "skip" && item.item.Exists) {
			job.TotalBytes += item.item.Size
		}
	}
	jobCtx, cancel := context.WithCancel(ctx)
	entry := &queueEntry{job: job, profile: s.pending.profile, plan: plan, policy: policy, ctx: jobCtx, cancel: cancel}
	entry.profile.Excludes = slices.Clone(entry.profile.Excludes)
	s.pending = nil
	s.queue = append(s.queue, entry)
	s.startQueueLocked()
	return s.queueStateLocked(), nil
}

func (s *Service) GetQueue() QueueState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queueStateLocked()
}

func (s *Service) queueStateLocked() QueueState {
	state := QueueState{Paused: s.queuePaused, Jobs: make([]QueueJob, len(s.queue))}
	for i, entry := range s.queue {
		state.Jobs[i] = entry.job
		state.Jobs[i].Items = slices.Clone(entry.job.Items)
	}
	return state
}

func (s *Service) SetQueuePaused(paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queuePaused = paused
	if !paused {
		s.startQueueLocked()
	}
}

func (s *Service) ClearFinishedQueue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = slices.DeleteFunc(s.queue, func(entry *queueEntry) bool {
		return entry.job.Status != "waiting" && entry.job.Status != "running"
	})
}

func (s *Service) CancelQueueJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.queue {
		if entry.job.ID != id {
			continue
		}
		entry.cancel()
		if entry.job.Status == "waiting" {
			failQueueItems(entry, "cancelled", "задание отменено до запуска")
		}
		return nil
	}
	return errors.New("задание очереди не найдено")
}

func (s *Service) startQueueLocked() {
	if s.queueWorker != nil || s.queuePaused || s.queueClosed {
		return
	}
	for _, entry := range s.queue {
		if entry.job.Status == "waiting" {
			s.queueWorker = make(chan struct{})
			go s.runQueue(s.queueWorker)
			return
		}
	}
}

func (s *Service) runQueue(done chan struct{}) {
	for {
		s.mu.Lock()
		var entry *queueEntry
		if !s.queuePaused && !s.queueClosed {
			for _, candidate := range s.queue {
				if candidate.job.Status == "waiting" {
					entry = candidate
					break
				}
			}
		}
		if entry == nil {
			s.queueWorker = nil
			close(done)
			s.mu.Unlock()
			return
		}
		entry.job.Status = "running"
		s.mu.Unlock()
		s.executeQueueEntry(entry)
	}
}

func (s *Service) executeQueueEntry(entry *queueEntry) {
	observer := &batchObserver{
		start: func(index int) {
			s.mu.Lock()
			defer s.mu.Unlock()
			entry.job.Items[index].Status = "running"
		},
		progress: func(index int, bytes int64) {
			s.mu.Lock()
			defer s.mu.Unlock()
			item := &entry.job.Items[index]
			bytes = min(bytes, max(int64(0), item.Size-item.TransferredBytes))
			item.TransferredBytes += bytes
			entry.job.TransferredBytes += bytes
		},
		finish: func(index int, result BatchItemResult) {
			s.mu.Lock()
			defer s.mu.Unlock()
			entry.job.Items[index].Status = result.Status
			entry.job.Items[index].Error = result.Error
			entry.job.ProcessedItems++
		},
	}
	ctx := context.WithValue(entry.ctx, batchObserverKey{}, observer)
	ctx = context.WithValue(ctx, queueWaitKey{}, true)
	started := false
	err := s.remoteOperation(ctx, entry.profile, 30*time.Minute, func(operationCtx context.Context, session *transport.Session) error {
		started = true
		executeBatch(operationCtx, entry.profile, entry.plan, entry.policy, session.Client)
		return nil
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	defer entry.cancel()
	if !started {
		status := "failed"
		if entry.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = "cancelled"
		}
		message := "не удалось запустить передачу"
		if err != nil {
			message = err.Error()
		}
		failQueueItems(entry, status, message)
		return
	}
	entry.job.Status = "completed"
	for _, item := range entry.job.Items {
		if item.Status == "cancelled" {
			entry.job.Status = "cancelled"
		}
	}
	for _, item := range entry.job.Items {
		if item.Status == "failed" {
			entry.job.Status = "failed"
			break
		}
	}
}

func failQueueItems(entry *queueEntry, status, message string) {
	entry.job.Status, entry.job.Error = status, message
	for index := range entry.job.Items {
		item := &entry.job.Items[index]
		if item.Status == "waiting" || item.Status == "running" {
			item.Status, item.Error = status, message
			entry.job.ProcessedItems++
		}
	}
}

// Retry builds a fresh plan using the original job profile, never the current
// server selection. Its new token still requires normal user confirmation.
func (s *Service) PreviewQueueRetry(ctx context.Context, id string) (BatchPreview, error) {
	s.mu.Lock()
	var profile config.Profile
	var direction string
	paths := []string{}
	for _, entry := range s.queue {
		if entry.job.ID != id {
			continue
		}
		if entry.job.Status == "waiting" || entry.job.Status == "running" {
			s.mu.Unlock()
			return BatchPreview{}, errors.New("дождитесь завершения задания")
		}
		profile, direction = entry.profile, entry.job.Direction
		profile.Excludes = slices.Clone(profile.Excludes)
		for _, item := range entry.job.Items {
			if item.Status == "failed" || item.Status == "cancelled" {
				paths = append(paths, item.RelativePath)
			}
		}
		break
	}
	s.mu.Unlock()
	if len(paths) == 0 {
		return BatchPreview{}, errors.New("нет файлов для повтора")
	}
	return s.previewBatchWithProfile(ctx, profile, direction, paths, "", "")
}

func (s *Service) shutdownQueue() {
	s.mu.Lock()
	s.queueClosed = true
	for _, entry := range s.queue {
		entry.cancel()
		if entry.job.Status == "waiting" {
			failQueueItems(entry, "cancelled", "приложение закрывается")
		}
	}
	done := s.queueWorker
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}
