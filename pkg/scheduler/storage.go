package scheduler

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hyp3rd/ewrap"
)

var errJobAlreadyExists = ewrap.New("job already exists")

// JobsStorage persists scheduled jobs metadata and runtime status/history state.
// Implementations must be safe for concurrent use.
//
//nolint:interfacebloat // centralized scheduler state contract intentionally groups active-job and status/history operations.
type JobsStorage interface {
	Save(job Job) error
	Delete(id string) bool
	Exists(id string) bool
	Count() int
	IDs() []string

	UpsertStatus(id string, state JobState) error
	MarkRemoved(id string) error
	MarkTerminal(id string, state JobState) error
	MarkExecutionStart(id string) error
	RecordExecutionResult(id string, payload CallbackPayload, historyLimit int) error
	State(id string) (JobState, bool)
	Status(id string) (JobStatus, bool)
	Statuses() []JobStatus
	History(id string) ([]JobRun, bool)
}

type jobStatusEntry struct {
	status       JobStatus
	history      []JobRun
	activeRuns   int
	terminal     bool
	lastSequence int
}

// InMemoryJobsStorage is the default in-memory JobsStorage implementation.
type InMemoryJobsStorage struct {
	mu     sync.RWMutex
	jobs   map[string]Job
	status map[string]*jobStatusEntry
}

// NewInMemoryJobsStorage creates an in-memory job storage.
func NewInMemoryJobsStorage() *InMemoryJobsStorage {
	return &InMemoryJobsStorage{
		jobs:   make(map[string]Job),
		status: make(map[string]*jobStatusEntry),
	}
}

// Save stores a job by ID.
func (s *InMemoryJobsStorage) Save(job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[job.ID]; exists {
		return ewrap.Wrapf(errJobAlreadyExists, "job ID: %s", job.ID)
	}

	s.jobs[job.ID] = job

	return nil
}

// Delete removes a job by ID.
func (s *InMemoryJobsStorage) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.jobs[id]; !ok {
		return false
	}

	delete(s.jobs, id)

	return true
}

// Exists reports whether a job exists by ID.
func (s *InMemoryJobsStorage) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.jobs[id]

	return exists
}

// Count returns the number of stored jobs.
func (s *InMemoryJobsStorage) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.jobs)
}

// IDs returns sorted stored job IDs.
func (s *InMemoryJobsStorage) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.jobs))
	for id := range s.jobs {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	return ids
}

// UpsertStatus creates or updates a job status entry with the provided state.
func (s *InMemoryJobsStorage) UpsertStatus(id string, state JobState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	entry, ok := s.status[id]
	if !ok {
		entry = &jobStatusEntry{
			status: JobStatus{
				JobID:     id,
				CreatedAt: now,
			},
		}
		s.status[id] = entry
	}

	entry.status.State = state
	entry.status.UpdatedAt = now

	return nil
}

// MarkRemoved sets a status entry to removed and terminal.
func (s *InMemoryJobsStorage) MarkRemoved(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.status[id]
	if !ok {
		return nil
	}

	entry.terminal = true
	entry.status.State = JobStateRemoved
	entry.status.ActiveRuns = entry.activeRuns
	entry.status.UpdatedAt = time.Now()

	return nil
}

// MarkTerminal marks a status entry as terminal with the provided state.
func (s *InMemoryJobsStorage) MarkTerminal(id string, state JobState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.status[id]
	if !ok || entry.terminal {
		return nil
	}

	entry.terminal = true
	entry.status.State = state
	entry.status.ActiveRuns = entry.activeRuns
	entry.status.UpdatedAt = time.Now()

	return nil
}

// MarkExecutionStart marks one execution as active for a job.
func (s *InMemoryJobsStorage) MarkExecutionStart(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.status[id]
	if !ok {
		return nil
	}

	entry.activeRuns++
	entry.status.ActiveRuns = entry.activeRuns
	entry.status.UpdatedAt = time.Now()

	if !entry.terminal {
		entry.status.State = JobStateRunning
	}

	return nil
}

// RecordExecutionResult appends run history and updates status counters.
func (s *InMemoryJobsStorage) RecordExecutionResult(id string, payload CallbackPayload, historyLimit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.status[id]
	if !ok {
		return nil
	}

	if historyLimit <= 0 {
		historyLimit = defaultHistoryLimit
	}

	entry.lastSequence++
	run := JobRun{
		Sequence: entry.lastSequence,
		Payload:  cloneCallbackPayload(payload),
	}

	entry.history = append(entry.history, run)
	if len(entry.history) > historyLimit {
		entry.history = entry.history[len(entry.history)-historyLimit:]
	}

	entry.status.Runs = entry.lastSequence
	lastRunCopy := cloneCallbackPayload(payload)
	entry.status.LastRun = &lastRunCopy

	if entry.activeRuns > 0 {
		entry.activeRuns--
	}

	entry.status.ActiveRuns = entry.activeRuns
	entry.status.UpdatedAt = time.Now()

	if !entry.terminal {
		if entry.activeRuns > 0 {
			entry.status.State = JobStateRunning
		} else {
			entry.status.State = JobStateScheduled
		}
	}

	return nil
}

// State returns the latest lifecycle state for a job status entry.
func (s *InMemoryJobsStorage) State(id string) (JobState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.status[id]
	if !ok {
		return "", false
	}

	return entry.status.State, true
}

// Status returns the latest status snapshot for a job.
func (s *InMemoryJobsStorage) Status(id string) (JobStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.status[id]
	if !ok {
		return JobStatus{}, false
	}

	return cloneJobStatus(entry.status), true
}

// Statuses returns all status snapshots sorted by job ID.
func (s *InMemoryJobsStorage) Statuses() []JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statuses := make([]JobStatus, 0, len(s.status))
	for _, entry := range s.status {
		statuses = append(statuses, cloneJobStatus(entry.status))
	}

	slices.SortFunc(statuses, func(a, b JobStatus) int {
		return strings.Compare(a.JobID, b.JobID)
	})

	return statuses
}

// History returns retained execution history for a job.
func (s *InMemoryJobsStorage) History(id string) ([]JobRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.status[id]
	if !ok {
		return nil, false
	}

	history := make([]JobRun, 0, len(entry.history))
	for _, run := range entry.history {
		history = append(history, cloneJobRun(run))
	}

	return history, true
}

func cloneJobStatus(status JobStatus) JobStatus {
	out := status
	if status.LastRun != nil {
		last := cloneCallbackPayload(*status.LastRun)
		out.LastRun = &last
	}

	return out
}

func cloneJobRun(run JobRun) JobRun {
	out := run
	out.Payload = cloneCallbackPayload(run.Payload)

	return out
}

func cloneCallbackPayload(payload CallbackPayload) CallbackPayload {
	return payload
}
