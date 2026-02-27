package scheduler

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hyp3rd/ewrap"
)

const (
	contractCountTwo        = 2
	contractCountThree      = 3
	contractCountFour       = 4
	contractHistoryLimitTwo = 2
	contractHistoryLimit3   = 3
	contractRunsPerWorker   = 5
	contractWorkers         = 24
	contractSequenceThree   = 3
	contractSequenceFour    = 4
	contractStatusCodeOK    = 200
)

const (
	contractJobA         = "job-a"
	contractJobB         = "job-b"
	contractJobC         = "job-c"
	contractJobLifecycle = "job-lifecycle"
	contractJobMissing   = "job-missing"
	contractJobOne       = "job-1"
	contractJobTwo       = "job-2"
	contractJobTen       = "job-10"
	contractJobRemoved   = "job-removed"
	contractJobTerminal  = "job-terminal"
	contractMutatedError = "mutated"
)

var errContractStatusMissing = errors.New("storage contract status missing")

type jobsStorageFactory func() JobsStorage

func TestInMemoryJobsStorageContract(t *testing.T) {
	t.Parallel()

	runJobsStorageContractTests(t, func() JobsStorage {
		return NewInMemoryJobsStorage()
	})
}

func TestSQLiteJobsStorageContract(t *testing.T) {
	t.Parallel()

	runJobsStorageContractTests(t, func() JobsStorage {
		return newSQLiteContractStorage(t)
	})
}

func TestSQLiteJobsStoragePersistsAcrossReopen(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "scheduler-state.db")

	storage, err := NewSQLiteJobsStorage(dbPath)
	requireNoError(t, err)
	t.Cleanup(func() {
		requireNoError(t, storage.Close())
	})

	jobID := "job-persisted"
	requireNoError(t, saveAndUpsertStatus(storage, jobID))
	seedHistoryRuns(t, storage, jobID, contractCountThree, contractCountThree)

	requireNoError(t, storage.Close())

	reopened, err := NewSQLiteJobsStorage(dbPath)
	requireNoError(t, err)
	t.Cleanup(func() {
		requireNoError(t, reopened.Close())
	})

	if got := reopened.Count(); got != 1 {
		t.Fatalf("expected 1 persisted job after reopen, got %d", got)
	}

	status := requireStatus(t, reopened, jobID)
	if status.Runs != contractCountThree {
		t.Fatalf("expected persisted status runs=%d, got %d", contractCountThree, status.Runs)
	}

	history := requireHistory(t, reopened, jobID)
	if len(history) != contractCountThree {
		t.Fatalf("expected %d persisted history entries, got %d", contractCountThree, len(history))
	}
}

func runJobsStorageContractTests(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	t.Run("jobs_crud", func(t *testing.T) {
		t.Parallel()
		testJobsStorageCRUD(t, factory)
	})

	t.Run("status_initialization", func(t *testing.T) {
		t.Parallel()
		testJobsStorageStatusInitialization(t, factory)
	})

	t.Run("status_execution_transitions", func(t *testing.T) {
		t.Parallel()
		testJobsStorageStatusExecutionTransitions(t, factory)
	})

	t.Run("status_defensive_copies", func(t *testing.T) {
		t.Parallel()
		testJobsStorageStatusDefensiveCopies(t, factory)
	})

	t.Run("history_retention_and_sorting", func(t *testing.T) {
		t.Parallel()
		testJobsStorageHistoryRetentionAndSorting(t, factory)
	})

	t.Run("terminal_behavior", func(t *testing.T) {
		t.Parallel()
		testJobsStorageTerminalBehavior(t, factory)
	})

	t.Run("concurrency", func(t *testing.T) {
		t.Parallel()
		testJobsStorageConcurrency(t, factory)
	})
}

func testJobsStorageCRUD(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	store := factory()

	requireNoError(t, store.Save(Job{ID: contractJobB}))
	requireNoError(t, store.Save(Job{ID: contractJobA}))
	requireNoError(t, store.Save(Job{ID: contractJobC}))

	err := store.Save(Job{ID: contractJobA})
	if err == nil || !errors.Is(err, errJobAlreadyExists) {
		t.Fatalf("expected duplicate save to wrap errJobAlreadyExists, got %v", err)
	}

	if got := store.Count(); got != contractCountThree {
		t.Fatalf("expected count %d, got %d", contractCountThree, got)
	}

	if !store.Exists(contractJobA) {
		t.Fatalf("expected %q to exist", contractJobA)
	}

	if store.Exists(contractJobMissing) {
		t.Fatalf("expected %q to not exist", contractJobMissing)
	}

	gotIDs := store.IDs()

	wantIDs := []string{contractJobA, contractJobB, contractJobC}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("expected sorted ids %v, got %v", wantIDs, gotIDs)
	}

	if !store.Delete(contractJobB) {
		t.Fatalf("expected first delete(%s) to return true", contractJobB)
	}

	if store.Delete(contractJobB) {
		t.Fatalf("expected second delete(%s) to return false", contractJobB)
	}

	if got := store.Count(); got != contractCountTwo {
		t.Fatalf("expected count %d after delete, got %d", contractCountTwo, got)
	}
}

func testJobsStorageStatusInitialization(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	store := newLifecycleStore(t, factory)

	status := requireStatus(t, store, contractJobLifecycle)

	if status.State != JobStateScheduled {
		t.Fatalf("expected scheduled state, got %q", status.State)
	}

	if status.CreatedAt.IsZero() || status.UpdatedAt.IsZero() {
		t.Fatalf("expected non-zero timestamps, got created=%v updated=%v", status.CreatedAt, status.UpdatedAt)
	}

	if status.Runs != 0 || status.ActiveRuns != 0 || status.LastRun != nil {
		t.Fatalf("expected empty run stats for new status, got %+v", status)
	}
}

func testJobsStorageStatusExecutionTransitions(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	store := newLifecycleStore(t, factory)

	requireNoError(t, store.MarkExecutionStart(contractJobLifecycle))

	status := requireStatus(t, store, contractJobLifecycle)
	if status.State != JobStateRunning || status.ActiveRuns != 1 {
		t.Fatalf("expected running/active=1 after mark start, got state=%q active=%d", status.State, status.ActiveRuns)
	}

	requireNoError(t, store.RecordExecutionResult(
		contractJobLifecycle,
		newContractPayload(contractJobLifecycle, 1),
		contractHistoryLimitTwo,
	))

	status = requireStatus(t, store, contractJobLifecycle)
	if status.State != JobStateScheduled || status.Runs != 1 || status.ActiveRuns != 0 || status.LastRun == nil {
		t.Fatalf("expected scheduled runs=1 active=0 last-run set, got %+v", status)
	}

	requireNoError(t, store.MarkExecutionStart(contractJobLifecycle))
	requireNoError(t, store.RecordExecutionResult(
		contractJobLifecycle,
		newContractPayload(contractJobLifecycle, contractCountTwo),
		contractHistoryLimitTwo,
	))

	history := requireHistory(t, store, contractJobLifecycle)
	if len(history) != contractCountTwo || history[0].Sequence != 1 || history[1].Sequence != contractCountTwo {
		t.Fatalf("expected history with sequences [1 2], got %+v", history)
	}

	state, ok := store.State(contractJobLifecycle)
	if !ok || state != JobStateScheduled {
		t.Fatalf("expected state scheduled, got ok=%t state=%q", ok, state)
	}
}

func testJobsStorageStatusDefensiveCopies(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	store := newLifecycleStore(t, factory)
	seedHistoryRuns(t, store, contractJobLifecycle, contractCountTwo, contractHistoryLimitTwo)

	statusCopy := requireStatus(t, store, contractJobLifecycle)
	statusCopy.LastRun.Success = false

	status := requireStatus(t, store, contractJobLifecycle)
	if status.LastRun == nil || !status.LastRun.Success {
		t.Fatalf("expected status copy to be defensive; got %+v", status.LastRun)
	}

	historyCopy := requireHistory(t, store, contractJobLifecycle)
	historyCopy[0].Payload.Error = contractMutatedError

	history := requireHistory(t, store, contractJobLifecycle)
	if history[0].Payload.Error == contractMutatedError {
		t.Fatal("expected history copy to be defensive")
	}
}

func testJobsStorageHistoryRetentionAndSorting(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	store := factory()

	requireNoError(t, saveAndUpsertStatus(store, contractJobTwo))
	requireNoError(t, saveAndUpsertStatus(store, contractJobTen))
	requireNoError(t, saveAndUpsertStatus(store, contractJobOne))

	statuses := store.Statuses()

	gotOrder := make([]string, 0, len(statuses))
	for _, status := range statuses {
		gotOrder = append(gotOrder, status.JobID)
	}

	wantOrder := []string{contractJobOne, contractJobTen, contractJobTwo}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Fatalf("expected sorted status order %v, got %v", wantOrder, gotOrder)
	}

	seedHistoryRuns(t, store, contractJobOne, contractCountFour, contractHistoryLimitTwo)

	history := requireHistory(t, store, contractJobOne)
	if len(history) != contractCountTwo {
		t.Fatalf("expected retained history length %d, got %d", contractCountTwo, len(history))
	}

	if history[0].Sequence != contractSequenceThree || history[1].Sequence != contractSequenceFour {
		t.Fatalf("expected retained sequences [%d %d], got [%d %d]",
			contractSequenceThree,
			contractSequenceFour,
			history[0].Sequence,
			history[1].Sequence,
		)
	}

	status := requireStatus(t, store, contractJobOne)
	if status.Runs != contractCountFour {
		t.Fatalf("expected runs=%d, got %d", contractCountFour, status.Runs)
	}

	if _, ok := store.History(contractJobMissing); ok {
		t.Fatalf("expected missing history lookup for %q to return ok=false", contractJobMissing)
	}
}

func testJobsStorageTerminalBehavior(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	store := factory()

	requireNoError(t, saveAndUpsertStatus(store, contractJobRemoved))
	requireNoError(t, store.MarkRemoved(contractJobRemoved))
	requireNoError(t, store.MarkTerminal(contractJobRemoved, JobStateStopped))

	removedStatus := requireStatus(t, store, contractJobRemoved)
	if removedStatus.State != JobStateRemoved {
		t.Fatalf("expected removed status to remain removed, got %q", removedStatus.State)
	}

	requireNoError(t, saveAndUpsertStatus(store, contractJobTerminal))
	requireNoError(t, store.MarkExecutionStart(contractJobTerminal))
	requireNoError(t, store.MarkTerminal(contractJobTerminal, JobStateCompleted))
	requireNoError(t, store.RecordExecutionResult(
		contractJobTerminal,
		newContractPayload(contractJobTerminal, 1),
		contractHistoryLimitTwo,
	))

	terminalStatus := requireStatus(t, store, contractJobTerminal)
	if terminalStatus.State != JobStateCompleted {
		t.Fatalf("expected terminal status to remain completed, got %q", terminalStatus.State)
	}
}

func testJobsStorageConcurrency(t *testing.T, factory jobsStorageFactory) {
	t.Helper()

	store := factory()

	errCh := make(chan error, contractWorkers)

	var wg sync.WaitGroup

	for i := range contractWorkers {
		jobID := fmt.Sprintf("job-concurrent-%d", i)

		wg.Go(func() {
			err := runConcurrentStorageFlow(store, jobID, contractRunsPerWorker)
			if err != nil {
				errCh <- err
			}
		})
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent storage flow failed: %v", err)
	}

	if got := store.Count(); got != contractWorkers {
		t.Fatalf("expected %d jobs after concurrent flow, got %d", contractWorkers, got)
	}

	statuses := store.Statuses()
	if len(statuses) != contractWorkers {
		t.Fatalf("expected %d statuses after concurrent flow, got %d", contractWorkers, len(statuses))
	}

	for _, status := range statuses {
		if status.Runs != contractRunsPerWorker {
			t.Fatalf("expected runs=%d for %s, got %d", contractRunsPerWorker, status.JobID, status.Runs)
		}
	}
}

func runConcurrentStorageFlow(store JobsStorage, id string, runs int) error {
	err := saveAndUpsertStatus(store, id)
	if err != nil {
		return ewrap.Wrapf(err, "setup failed for %s", id)
	}

	for run := range runs {
		err = store.MarkExecutionStart(id)
		if err != nil {
			return ewrap.Wrapf(err, "mark start failed for %s", id)
		}

		payload := newContractPayload(id, run+1)

		err = store.RecordExecutionResult(id, payload, contractHistoryLimit3)
		if err != nil {
			return ewrap.Wrapf(err, "record result failed for %s", id)
		}

		_, ok := store.Status(id)
		if !ok {
			return ewrap.Wrapf(errContractStatusMissing, "job id: %s", id)
		}

		_ = store.IDs()
		_ = store.Statuses()
	}

	return nil
}

func newLifecycleStore(t *testing.T, factory jobsStorageFactory) JobsStorage {
	t.Helper()

	store := factory()

	requireNoError(t, saveAndUpsertStatus(store, contractJobLifecycle))

	return store
}

func saveAndUpsertStatus(store JobsStorage, id string) error {
	err := store.Save(Job{ID: id})
	if err != nil {
		return err
	}

	err = store.UpsertStatus(id, JobStateScheduled)
	if err != nil {
		return err
	}

	return nil
}

func newSQLiteContractStorage(t *testing.T) JobsStorage {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "storage-contract.db")

	storage, err := NewSQLiteJobsStorage(dbPath)
	requireNoError(t, err)

	t.Cleanup(func() {
		requireNoError(t, storage.Close())
	})

	return storage
}

func seedHistoryRuns(t *testing.T, store JobsStorage, id string, runs, historyLimit int) {
	t.Helper()

	for run := range runs {
		requireNoError(t, store.MarkExecutionStart(id))
		requireNoError(t, store.RecordExecutionResult(id, newContractPayload(id, run+1), historyLimit))
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func requireStatus(t *testing.T, store JobsStorage, id string) JobStatus {
	t.Helper()

	status, ok := store.Status(id)
	if !ok {
		t.Fatalf("expected status for %q", id)
	}

	return status
}

func requireHistory(t *testing.T, store JobsStorage, id string) []JobRun {
	t.Helper()

	history, ok := store.History(id)
	if !ok {
		t.Fatalf("expected history for %q", id)
	}

	return history
}

func newContractPayload(jobID string, run int) CallbackPayload {
	now := time.Now().UTC()

	return CallbackPayload{
		JobID:        jobID,
		ScheduledAt:  now,
		StartedAt:    now,
		FinishedAt:   now.Add(1 * time.Millisecond),
		Attempts:     run,
		Success:      true,
		StatusCode:   contractStatusCodeOK,
		Error:        "",
		ResponseBody: fmt.Sprintf("run-%d", run),
	}
}
