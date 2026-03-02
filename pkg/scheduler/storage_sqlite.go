package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/hyp3rd/ewrap"

	_ "modernc.org/sqlite" // SQLite database/sql driver.
)

const (
	sqliteDriverName     = "sqlite"
	sqlitePragmaWAL      = "PRAGMA journal_mode = WAL;"
	sqlitePragmaBusy     = "PRAGMA busy_timeout = 5000;"
	sqlitePragmaForeign  = "PRAGMA foreign_keys = ON;"
	sqliteMaxOpenConns   = 1
	sqliteDefaultDataDSN = "file"
)

const (
	sqlCreateJobsTable = `
CREATE TABLE IF NOT EXISTS scheduler_jobs (
	id TEXT PRIMARY KEY,
	job_json BLOB NOT NULL
);`
	sqlCreateStatusTable = `
CREATE TABLE IF NOT EXISTS scheduler_job_status (
	id TEXT PRIMARY KEY,
	status_json BLOB NOT NULL,
	active_runs INTEGER NOT NULL,
	terminal INTEGER NOT NULL,
	last_sequence INTEGER NOT NULL
);`
	sqlCreateHistoryTable = `
CREATE TABLE IF NOT EXISTS scheduler_job_history (
	id TEXT NOT NULL,
	sequence INTEGER NOT NULL,
	payload_json BLOB NOT NULL,
	finished_at_unix_nano INTEGER NOT NULL,
	PRIMARY KEY (id, sequence)
);`
	sqlCreateHistoryIndex = `
CREATE INDEX IF NOT EXISTS idx_scheduler_job_history_id_seq
ON scheduler_job_history (id, sequence);`
	sqlAlterHistoryAddFinishedAt = `
ALTER TABLE scheduler_job_history ADD COLUMN finished_at_unix_nano INTEGER NOT NULL DEFAULT 0;`
)

// SQLiteJobsStorage stores scheduler state in SQLite.
type SQLiteJobsStorage struct {
	db               *sql.DB
	writeMu          sync.Mutex
	historyRetention SQLiteHistoryRetentionOptions
}

// SQLiteHistoryRetentionOptions configures SQLite history retention behavior.
type SQLiteHistoryRetentionOptions struct {
	// MaxAge keeps runs newer than now-MaxAge when > 0.
	MaxAge time.Duration
	// MaxRowsPerJob keeps only the latest N runs per job when > 0.
	MaxRowsPerJob int
}

// SQLiteJobsStorageOption configures SQLiteJobsStorage behavior.
type SQLiteJobsStorageOption func(*SQLiteJobsStorage)

type sqliteJobData struct {
	ID string `json:"id"`
}

type sqliteStatusData struct {
	JobID      string           `json:"job_id"`
	State      JobState         `json:"state"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Runs       int              `json:"runs"`
	ActiveRuns int              `json:"active_runs"`
	LastRun    *CallbackPayload `json:"last_run,omitempty"`
}

type sqliteStatusRecord struct {
	status       sqliteStatusData
	activeRuns   int
	terminal     bool
	lastSequence int
	found        bool
}

// NewSQLiteJobsStorage creates a SQLite-backed jobs storage.
// The path can be a filesystem path or SQLite DSN.
func NewSQLiteJobsStorage(ctx context.Context, path string) (*SQLiteJobsStorage, error) {
	return NewSQLiteJobsStorageWithOptions(ctx, path)
}

// NewSQLiteJobsStorageWithOptions creates a SQLite-backed jobs storage with retention options.
// The path can be a filesystem path or SQLite DSN.
func NewSQLiteJobsStorageWithOptions(ctx context.Context, path string, opts ...SQLiteJobsStorageOption) (*SQLiteJobsStorage, error) {
	dsn := normalizeSQLitePath(path)

	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, ewrap.Wrapf(ErrStorageOperation, "open sqlite failed: %v", err)
	}

	db.SetMaxOpenConns(sqliteMaxOpenConns)

	storage := &SQLiteJobsStorage{
		db: db,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(storage)
		}
	}

	err = storage.bootstrap(ctx)
	if err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			err = errors.Join(err, ewrap.Wrapf(ErrStorageOperation, "close sqlite after init failure: %v", closeErr))
		}

		return nil, err
	}

	return storage, nil
}

// WithSQLiteHistoryRetention configures SQLite history retention.
func WithSQLiteHistoryRetention(retention SQLiteHistoryRetentionOptions) SQLiteJobsStorageOption {
	return func(s *SQLiteJobsStorage) {
		if retention.MaxAge > 0 {
			s.historyRetention.MaxAge = retention.MaxAge
		}

		if retention.MaxRowsPerJob > 0 {
			s.historyRetention.MaxRowsPerJob = retention.MaxRowsPerJob
		}
	}
}

// WithSQLiteHistoryMaxAge configures age-based SQLite history retention.
func WithSQLiteHistoryMaxAge(maxAge time.Duration) SQLiteJobsStorageOption {
	return func(s *SQLiteJobsStorage) {
		if maxAge > 0 {
			s.historyRetention.MaxAge = maxAge
		}
	}
}

// WithSQLiteHistoryMaxRowsPerJob configures row-count SQLite history retention.
func WithSQLiteHistoryMaxRowsPerJob(maxRowsPerJob int) SQLiteJobsStorageOption {
	return func(s *SQLiteJobsStorage) {
		if maxRowsPerJob > 0 {
			s.historyRetention.MaxRowsPerJob = maxRowsPerJob
		}
	}
}

// Close closes the underlying SQLite DB handle.
func (s *SQLiteJobsStorage) Close() error {
	err := s.db.Close()
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "close sqlite failed: %v", err)
	}

	return nil
}

// Save stores a job by ID.
func (s *SQLiteJobsStorage) Save(ctx context.Context, job Job) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(sqliteJobData{ID: job.ID})
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "marshal job data failed: %v", err)
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO scheduler_jobs (id, job_json) VALUES (?, ?)`,
		job.ID,
		data,
	)
	if err != nil {
		if isSQLiteUniqueViolation(err) {
			return ewrap.Wrapf(errJobAlreadyExists, "job ID: %s", job.ID)
		}

		return ewrap.Wrapf(ErrStorageOperation, "insert job failed: %v", err)
	}

	return nil
}

// Delete removes a job by ID.
func (s *SQLiteJobsStorage) Delete(ctx context.Context, id string) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM scheduler_jobs WHERE id = ?`, id)
	if err != nil {
		return false
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false
	}

	return affected > 0
}

// Exists reports whether a job exists by ID.
func (s *SQLiteJobsStorage) Exists(ctx context.Context, id string) bool {
	var found int

	err := s.db.QueryRowContext(
		ctx,
		`SELECT 1 FROM scheduler_jobs WHERE id = ? LIMIT 1`,
		id,
	).Scan(&found)
	if err != nil {
		return false
	}

	return found == 1
}

// Count returns the number of stored jobs.
func (s *SQLiteJobsStorage) Count(ctx context.Context) int {
	var count int

	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_jobs`).Scan(&count)
	if err != nil {
		return 0
	}

	return count
}

// IDs returns sorted stored job IDs.
func (s *SQLiteJobsStorage) IDs(ctx context.Context) []string {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM scheduler_jobs ORDER BY id ASC`)
	if err != nil {
		return nil
	}

	ids := make([]string, 0)

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			ids = nil
		}
	}()

	for rows.Next() {
		var id string

		err = rows.Scan(&id)
		if err != nil {
			return nil
		}

		ids = append(ids, id)
	}

	err = rows.Err()
	if err != nil {
		return nil
	}

	return ids
}

// UpsertStatus creates or updates a job status entry with the provided state.
func (s *SQLiteJobsStorage) UpsertStatus(ctx context.Context, id string, state JobState) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.withWriteTx(ctx, "upsert status", func(ctx context.Context, tx *sql.Tx) error {
		record, err := loadStatusRecordTx(ctx, tx, id)
		if err != nil {
			return err
		}

		now := time.Now()
		if !record.found {
			record.status = sqliteStatusData{
				JobID:     id,
				CreatedAt: now,
			}
			record.activeRuns = 0
			record.terminal = false
			record.lastSequence = 0
		}

		record.status.State = state
		record.status.UpdatedAt = now

		return upsertStatusRecordTx(ctx, tx, id, record)
	})
}

// MarkRemoved sets a status entry to removed and terminal.
func (s *SQLiteJobsStorage) MarkRemoved(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.withWriteTx(ctx, "mark removed", func(ctx context.Context, tx *sql.Tx) error {
		record, err := loadStatusRecordTx(ctx, tx, id)
		if err != nil {
			return err
		}

		if !record.found {
			return nil
		}

		record.terminal = true
		record.status.State = JobStateRemoved
		record.status.ActiveRuns = record.activeRuns
		record.status.UpdatedAt = time.Now()

		return upsertStatusRecordTx(ctx, tx, id, record)
	})
}

// MarkTerminal marks a status entry as terminal with the provided state.
func (s *SQLiteJobsStorage) MarkTerminal(ctx context.Context, id string, state JobState) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.withWriteTx(ctx, "mark terminal", func(ctx context.Context, tx *sql.Tx) error {
		record, err := loadStatusRecordTx(ctx, tx, id)
		if err != nil {
			return err
		}

		if !record.found || record.terminal {
			return nil
		}

		record.terminal = true
		record.status.State = state
		record.status.ActiveRuns = record.activeRuns
		record.status.UpdatedAt = time.Now()

		return upsertStatusRecordTx(ctx, tx, id, record)
	})
}

// MarkExecutionStart marks one execution as active for a job.
func (s *SQLiteJobsStorage) MarkExecutionStart(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.withWriteTx(ctx, "mark execution start", func(ctx context.Context, tx *sql.Tx) error {
		record, err := loadStatusRecordTx(ctx, tx, id)
		if err != nil {
			return err
		}

		if !record.found {
			return nil
		}

		record.activeRuns++
		record.status.ActiveRuns = record.activeRuns
		record.status.UpdatedAt = time.Now()

		if !record.terminal {
			record.status.State = JobStateRunning
		}

		return upsertStatusRecordTx(ctx, tx, id, record)
	})
}

// RecordExecutionResult appends run history and updates status counters.
func (s *SQLiteJobsStorage) RecordExecutionResult(ctx context.Context, id string, payload CallbackPayload, historyLimit int) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.withWriteTx(ctx, "record execution result", func(ctx context.Context, tx *sql.Tx) error {
		record, err := loadStatusRecordTx(ctx, tx, id)
		if err != nil {
			return err
		}

		if !record.found {
			return nil
		}

		if historyLimit <= 0 {
			historyLimit = defaultHistoryLimit
		}

		effectiveHistoryLimit := mergeHistoryLimit(historyLimit, s.historyRetention.MaxRowsPerJob)
		record.lastSequence++

		err = insertHistoryTx(ctx, tx, id, record.lastSequence, payload)
		if err != nil {
			return err
		}

		err = trimHistoryTx(ctx, tx, id, record.lastSequence, effectiveHistoryLimit)
		if err != nil {
			return err
		}

		if s.historyRetention.MaxAge > 0 {
			cutoff := time.Now().Add(-s.historyRetention.MaxAge).UnixNano()

			_, err = pruneHistoryOlderThanForJobTx(ctx, tx, id, cutoff)
			if err != nil {
				return err
			}
		}

		record = applyExecutionResult(record, payload)

		return upsertStatusRecordTx(ctx, tx, id, record)
	})
}

// State returns the latest lifecycle state for a job status entry.
func (s *SQLiteJobsStorage) State(ctx context.Context, id string) (JobState, bool) {
	record, ok := s.loadStatusRecord(ctx, id)
	if !ok {
		return "", false
	}

	return record.status.State, true
}

// Status returns the latest status snapshot for a job.
func (s *SQLiteJobsStorage) Status(ctx context.Context, id string) (JobStatus, bool) {
	record, ok := s.loadStatusRecord(ctx, id)
	if !ok {
		return JobStatus{}, false
	}

	status := sqliteStatusToJobStatus(record.status, record.activeRuns)

	return cloneJobStatus(status), true
}

// Statuses returns all status snapshots sorted by job ID.
func (s *SQLiteJobsStorage) Statuses(ctx context.Context) []JobStatus {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, status_json, active_runs
FROM scheduler_job_status
ORDER BY id ASC
`)
	if err != nil {
		return nil
	}

	statuses := make([]JobStatus, 0)

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			statuses = nil
		}
	}()

	for rows.Next() {
		var (
			id         string
			statusData []byte
			activeRuns int
		)

		err = rows.Scan(&id, &statusData, &activeRuns)
		if err != nil {
			return nil
		}

		status, decodeErr := decodeSQLiteStatus(statusData, activeRuns)
		if decodeErr != nil {
			return nil
		}

		status.JobID = id
		statuses = append(statuses, cloneJobStatus(status))
	}

	err = rows.Err()
	if err != nil {
		return nil
	}

	return statuses
}

// History returns retained execution history for a job.
func (s *SQLiteJobsStorage) History(ctx context.Context, id string) ([]JobRun, bool) {
	rows, err := s.db.QueryContext(ctx, `
SELECT sequence, payload_json
FROM scheduler_job_history
WHERE id = ?
ORDER BY sequence ASC
`, id)
	if err != nil {
		return nil, false
	}

	history := make([]JobRun, 0)

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			history = nil
		}
	}()

	for rows.Next() {
		var (
			sequence int
			data     []byte
			payload  CallbackPayload
		)

		err = rows.Scan(&sequence, &data)
		if err != nil {
			return nil, false
		}

		err = json.Unmarshal(data, &payload)
		if err != nil {
			return nil, false
		}

		history = append(history, JobRun{
			Sequence: sequence,
			Payload:  cloneCallbackPayload(payload),
		})
	}

	err = rows.Err()
	if err != nil {
		return nil, false
	}

	if len(history) == 0 {
		return nil, s.statusExists(ctx, id)
	}

	return history, true
}

// PruneHistory applies configured retention rules and returns number of deleted history rows.
func (s *SQLiteJobsStorage) PruneHistory(ctx context.Context) (int, error) {
	return s.PruneHistoryWithRetention(ctx, s.historyRetention)
}

// PruneHistoryWithRetention applies one-off retention rules and returns number of deleted history rows.
func (s *SQLiteJobsStorage) PruneHistoryWithRetention(ctx context.Context, retention SQLiteHistoryRetentionOptions) (int, error) {
	if retention.MaxAge <= 0 && retention.MaxRowsPerJob <= 0 {
		return 0, nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var deleted int64

	err := s.withWriteTx(ctx, "prune history", func(ctx context.Context, tx *sql.Tx) error {
		if retention.MaxAge > 0 {
			cutoff := time.Now().Add(-retention.MaxAge).UnixNano()

			deletedByAge, pruneErr := pruneHistoryOlderThanTx(ctx, tx, cutoff)
			if pruneErr != nil {
				return pruneErr
			}

			deleted += deletedByAge
		}

		if retention.MaxRowsPerJob > 0 {
			deletedByRows, pruneErr := pruneHistoryMaxRowsPerJobTx(ctx, tx, retention.MaxRowsPerJob)
			if pruneErr != nil {
				return pruneErr
			}

			deleted += deletedByRows
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return int(deleted), nil
}

func (s *SQLiteJobsStorage) withWriteTx(
	ctx context.Context,
	operation string,
	fn func(context.Context, *sql.Tx) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "begin %s tx failed: %v", operation, err)
	}
	defer rollbackTx(tx)

	err = fn(ctx, tx)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "commit %s failed: %v", operation, err)
	}

	return nil
}

func (s *SQLiteJobsStorage) bootstrap(ctx context.Context) error {
	statements := []string{
		sqlitePragmaWAL,
		sqlitePragmaBusy,
		sqlitePragmaForeign,
		sqlCreateJobsTable,
		sqlCreateStatusTable,
		sqlCreateHistoryTable,
		sqlCreateHistoryIndex,
		sqlAlterHistoryAddFinishedAt,
	}

	for _, statement := range statements {
		_, err := s.db.ExecContext(ctx, statement)
		if err != nil {
			if statement == sqlAlterHistoryAddFinishedAt && isSQLiteDuplicateColumnError(err) {
				continue
			}

			return ewrap.Wrapf(ErrStorageOperation, "sqlite init statement failed: %v", err)
		}
	}

	return nil
}

func normalizeSQLitePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ":memory:"
	}

	if strings.Contains(trimmed, ":") {
		return trimmed
	}

	return fmt.Sprintf("%s:%s", sqliteDefaultDataDSN, trimmed)
}

func (s *SQLiteJobsStorage) loadStatusRecord(ctx context.Context, id string) (sqliteStatusRecord, bool) {
	record, err := loadStatusRecordQueryRow(s.db.QueryRowContext(
		ctx,
		`SELECT status_json, active_runs, terminal, last_sequence
FROM scheduler_job_status
WHERE id = ?`,
		id,
	))
	if err != nil || !record.found {
		return sqliteStatusRecord{}, false
	}

	return record, true
}

func (s *SQLiteJobsStorage) statusExists(ctx context.Context, id string) bool {
	var found int

	err := s.db.QueryRowContext(
		ctx,
		`SELECT 1 FROM scheduler_job_status WHERE id = ? LIMIT 1`,
		id,
	).Scan(&found)
	if err != nil {
		return false
	}

	return found == 1
}

func decodeSQLiteStatus(data []byte, activeRuns int) (JobStatus, error) {
	var sqliteStatus sqliteStatusData

	err := json.Unmarshal(data, &sqliteStatus)
	if err != nil {
		return JobStatus{}, ewrap.Wrapf(ErrStorageOperation, "unmarshal status failed: %v", err)
	}

	return sqliteStatusToJobStatus(sqliteStatus, activeRuns), nil
}

func sqliteStatusToJobStatus(data sqliteStatusData, activeRuns int) JobStatus {
	status := JobStatus{
		JobID:      data.JobID,
		State:      data.State,
		CreatedAt:  data.CreatedAt,
		UpdatedAt:  data.UpdatedAt,
		Runs:       data.Runs,
		ActiveRuns: activeRuns,
	}

	if data.LastRun != nil {
		lastRun := cloneCallbackPayload(*data.LastRun)
		status.LastRun = &lastRun
	}

	return status
}

func jobStatusToSQLiteStatus(status JobStatus) sqliteStatusData {
	out := sqliteStatusData{
		JobID:      status.JobID,
		State:      status.State,
		CreatedAt:  status.CreatedAt,
		UpdatedAt:  status.UpdatedAt,
		Runs:       status.Runs,
		ActiveRuns: status.ActiveRuns,
	}

	if status.LastRun != nil {
		lastRun := cloneCallbackPayload(*status.LastRun)
		out.LastRun = &lastRun
	}

	return out
}

func applyExecutionResult(record sqliteStatusRecord, payload CallbackPayload) sqliteStatusRecord {
	record.status.Runs = record.lastSequence

	lastRunCopy := cloneCallbackPayload(payload)
	record.status.LastRun = &lastRunCopy

	if record.activeRuns > 0 {
		record.activeRuns--
	}

	record.status.ActiveRuns = record.activeRuns
	record.status.UpdatedAt = time.Now()

	if !record.terminal {
		if record.activeRuns > 0 {
			record.status.State = JobStateRunning
		} else {
			record.status.State = JobStateScheduled
		}
	}

	return record
}

func loadStatusRecordTx(ctx context.Context, tx *sql.Tx, id string) (sqliteStatusRecord, error) {
	row := tx.QueryRowContext(
		ctx,
		`SELECT status_json, active_runs, terminal, last_sequence
FROM scheduler_job_status
WHERE id = ?`,
		id,
	)

	record, err := loadStatusRecordQueryRow(row)
	if err != nil {
		return sqliteStatusRecord{}, err
	}

	return record, nil
}

func loadStatusRecordQueryRow(row *sql.Row) (sqliteStatusRecord, error) {
	var (
		data         []byte
		activeRuns   int
		terminalFlag int
		lastSequence int
	)

	err := row.Scan(&data, &activeRuns, &terminalFlag, &lastSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return sqliteStatusRecord{found: false}, nil
	}

	if err != nil {
		return sqliteStatusRecord{}, ewrap.Wrapf(ErrStorageOperation, "load status failed: %v", err)
	}

	status, err := decodeSQLiteStatus(data, activeRuns)
	if err != nil {
		return sqliteStatusRecord{}, err
	}

	return sqliteStatusRecord{
		status:       jobStatusToSQLiteStatus(status),
		activeRuns:   activeRuns,
		terminal:     terminalFlag == 1,
		lastSequence: lastSequence,
		found:        true,
	}, nil
}

func upsertStatusRecordTx(ctx context.Context, tx *sql.Tx, id string, record sqliteStatusRecord) error {
	record.status.JobID = id
	record.status.ActiveRuns = record.activeRuns

	statusData, err := json.Marshal(record.status)
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "marshal status failed: %v", err)
	}

	terminalFlag := 0
	if record.terminal {
		terminalFlag = 1
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO scheduler_job_status (id, status_json, active_runs, terminal, last_sequence)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	status_json = excluded.status_json,
	active_runs = excluded.active_runs,
	terminal = excluded.terminal,
	last_sequence = excluded.last_sequence
`, id, statusData, record.activeRuns, terminalFlag, record.lastSequence)
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "upsert status failed: %v", err)
	}

	return nil
}

func insertHistoryTx(ctx context.Context, tx *sql.Tx, id string, sequence int, payload CallbackPayload) error {
	payloadData, err := json.Marshal(cloneCallbackPayload(payload))
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "marshal execution payload failed: %v", err)
	}

	finishedAtUnixNano := payloadFinishedAtUnixNano(payload)

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO scheduler_job_history (id, sequence, payload_json, finished_at_unix_nano) VALUES (?, ?, ?, ?)`,
		id,
		sequence,
		payloadData,
		finishedAtUnixNano,
	)
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "insert execution history failed: %v", err)
	}

	return nil
}

func trimHistoryTx(ctx context.Context, tx *sql.Tx, id string, sequence, historyLimit int) error {
	trimBefore := sequence - historyLimit
	if trimBefore <= 0 {
		return nil
	}

	_, err := tx.ExecContext(
		ctx,
		`DELETE FROM scheduler_job_history WHERE id = ? AND sequence <= ?`,
		id,
		trimBefore,
	)
	if err != nil {
		return ewrap.Wrapf(ErrStorageOperation, "trim execution history failed: %v", err)
	}

	return nil
}

func pruneHistoryOlderThanForJobTx(ctx context.Context, tx *sql.Tx, id string, cutoffUnixNano int64) (int64, error) {
	res, err := tx.ExecContext(
		ctx,
		`DELETE FROM scheduler_job_history WHERE id = ? AND finished_at_unix_nano > 0 AND finished_at_unix_nano < ?`,
		id,
		cutoffUnixNano,
	)
	if err != nil {
		return 0, ewrap.Wrapf(ErrStorageOperation, "prune execution history by age for job failed: %v", err)
	}

	return rowsAffected(res)
}

func pruneHistoryOlderThanTx(ctx context.Context, tx *sql.Tx, cutoffUnixNano int64) (int64, error) {
	res, err := tx.ExecContext(
		ctx,
		`DELETE FROM scheduler_job_history WHERE finished_at_unix_nano > 0 AND finished_at_unix_nano < ?`,
		cutoffUnixNano,
	)
	if err != nil {
		return 0, ewrap.Wrapf(ErrStorageOperation, "prune execution history by age failed: %v", err)
	}

	return rowsAffected(res)
}

func pruneHistoryMaxRowsPerJobTx(ctx context.Context, tx *sql.Tx, maxRowsPerJob int) (int64, error) {
	res, err := tx.ExecContext(
		ctx,
		`DELETE FROM scheduler_job_history
WHERE rowid IN (
	SELECT rowid FROM (
		SELECT rowid,
			ROW_NUMBER() OVER (PARTITION BY id ORDER BY sequence DESC) AS rn
		FROM scheduler_job_history
	) ranked
	WHERE rn > ?
)`,
		maxRowsPerJob,
	)
	if err != nil {
		return 0, ewrap.Wrapf(ErrStorageOperation, "prune execution history by max rows failed: %v", err)
	}

	return rowsAffected(res)
}

func mergeHistoryLimit(historyLimit, maxRowsPerJob int) int {
	if maxRowsPerJob <= 0 {
		return historyLimit
	}

	if historyLimit <= 0 {
		return maxRowsPerJob
	}

	return min(historyLimit, maxRowsPerJob)
}

func payloadFinishedAtUnixNano(payload CallbackPayload) int64 {
	if payload.FinishedAt.IsZero() {
		return 0
	}

	return payload.FinishedAt.UnixNano()
}

func rowsAffected(res sql.Result) (int64, error) {
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, ewrap.Wrapf(ErrStorageOperation, "rows affected failed: %v", err)
	}

	return affected, nil
}

func rollbackTx(tx *sql.Tx) {
	err := tx.Rollback()
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		return
	}
}

func isSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "constraint failed") && strings.Contains(msg, "unique")
}

func isSQLiteDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "duplicate column name")
}
