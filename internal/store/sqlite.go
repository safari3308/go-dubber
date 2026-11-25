package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// QueueRow is the persisted SQLite representation of a queued video item.
type QueueRow struct {
	ID                     string
	Position               int
	SourcePath             string
	FileName               string
	Status                 string
	Stage                  string
	Progress               float64
	Detail                 string
	Error                  string
	ConfigJSON             string
	AvailableLanguagesJSON string
	VideoInfoJSON          string
	MetricsJSON            string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// LogRecord is a persisted activity log entry.
type LogRecord struct {
	ID        int64  `json:"id"`
	JobID     string `json:"job_id"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// NotificationRecord is a persisted user notification (so missed toasts are delivered when returning to the tab).
type NotificationRecord struct {
	ID        int64  `json:"id"`
	Level     string `json:"level"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Read      bool   `json:"read"`
	Timestamp string `json:"timestamp"`
}

// DB wraps a pure-Go SQLite database stored inside workdir/dubber.db.
type DB struct {
	sqlDB *sql.DB
}

// Open initializes (or opens) the SQLite database in WAL mode and runs schema migrations.
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}
	// SQLite works best with a single writer connection in Go to avoid SQLITE_BUSY contention.
	sqlDB.SetMaxOpenConns(1)

	db := &DB{sqlDB: sqlDB}
	if err := db.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the underlying SQLite connection.
func (d *DB) Close() error {
	if d == nil || d.sqlDB == nil {
		return nil
	}
	return d.sqlDB.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS queue_items (
		id TEXT PRIMARY KEY,
		position INTEGER NOT NULL,
		source_path TEXT NOT NULL,
		file_name TEXT NOT NULL,
		status TEXT NOT NULL,
		stage TEXT NOT NULL DEFAULT '',
		progress REAL NOT NULL DEFAULT 0,
		detail TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		config_json TEXT NOT NULL,
		available_languages_json TEXT NOT NULL DEFAULT '[]',
		video_info_json TEXT NOT NULL DEFAULT '',
		metrics_json TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id TEXT NOT NULL DEFAULT '',
		level TEXT NOT NULL,
		message TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_logs_created_at ON logs(id DESC);

	CREATE TABLE IF NOT EXISTS notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		level TEXT NOT NULL,
		title TEXT NOT NULL,
		message TEXT NOT NULL,
		read INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_notifications_read ON notifications(read, id ASC);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	`
	_, err := d.sqlDB.Exec(schema)
	return err
}

// LoadSettingsJSON retrieves the persisted global config JSON from SQLite.
func (d *DB) LoadSettingsJSON() (string, bool, error) {
	var val string
	err := d.sqlDB.QueryRow(`SELECT value FROM settings WHERE key = 'global_config'`).Scan(&val)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

// SaveSettingsJSON upserts the global config JSON into SQLite.
func (d *DB) SaveSettingsJSON(rawJSON string) error {
	ts := time.Now().UTC().Format(time.RFC3339)
	_, err := d.sqlDB.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES ('global_config', ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		rawJSON, ts,
	)
	return err
}

// ReplaceQueue atomically replaces the entire queue table inside a single SQLite transaction.
func (d *DB) ReplaceQueue(rows []QueueRow) error {
	tx, err := d.sqlDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM queue_items`); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO queue_items (
			id, position, source_path, file_name, status, stage, progress, detail, error,
			config_json, available_languages_json, video_info_json, metrics_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, r := range rows {
		_, err := stmt.Exec(
			r.ID,
			i,
			r.SourcePath,
			r.FileName,
			r.Status,
			r.Stage,
			r.Progress,
			r.Detail,
			r.Error,
			r.ConfigJSON,
			r.AvailableLanguagesJSON,
			r.VideoInfoJSON,
			r.MetricsJSON,
			r.CreatedAt.UTC().Format(time.RFC3339Nano),
			r.UpdatedAt.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// LoadQueue returns all persisted queue items ordered by position.
func (d *DB) LoadQueue() ([]QueueRow, error) {
	rows, err := d.sqlDB.Query(`
		SELECT id, position, source_path, file_name, status, stage, progress, detail, error,
		       config_json, available_languages_json, video_info_json, metrics_json, created_at, updated_at
		FROM queue_items
		ORDER BY position ASC, created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []QueueRow
	for rows.Next() {
		var r QueueRow
		var createdStr, updatedStr string
		if err := rows.Scan(
			&r.ID, &r.Position, &r.SourcePath, &r.FileName, &r.Status, &r.Stage,
			&r.Progress, &r.Detail, &r.Error, &r.ConfigJSON, &r.AvailableLanguagesJSON,
			&r.VideoInfoJSON, &r.MetricsJSON, &createdStr, &updatedStr,
		); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

// InsertLog persists an activity log entry and prunes old entries beyond 1000 rows.
func (d *DB) InsertLog(level, jobID, message string) (LogRecord, error) {
	ts := time.Now().UTC().Format(time.RFC3339)
	res, err := d.sqlDB.Exec(
		`INSERT INTO logs (job_id, level, message, created_at) VALUES (?, ?, ?, ?)`,
		jobID, level, message, ts,
	)
	if err != nil {
		return LogRecord{}, err
	}
	id, _ := res.LastInsertId()

	// Periodically prune old logs to keep dubber.db compact
	if id%100 == 0 {
		_, _ = d.sqlDB.Exec(`DELETE FROM logs WHERE id <= (SELECT COALESCE(MAX(id), 0) - 1000 FROM logs)`)
	}

	return LogRecord{
		ID:        id,
		JobID:     jobID,
		Level:     level,
		Message:   message,
		Timestamp: ts,
	}, nil
}

// ListLogs returns the most recent N log entries (newest first).
func (d *DB) ListLogs(limit int) ([]LogRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.sqlDB.Query(
		`SELECT id, job_id, level, message, created_at FROM logs ORDER BY id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]LogRecord, 0)
	for rows.Next() {
		var r LogRecord
		if err := rows.Scan(&r.ID, &r.JobID, &r.Level, &r.Message, &r.Timestamp); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ClearLogs removes all persisted activity logs.
func (d *DB) ClearLogs() error {
	_, err := d.sqlDB.Exec(`DELETE FROM logs`)
	return err
}

// InsertNotification persists a notification so it can be delivered even if the browser tab was hidden.
func (d *DB) InsertNotification(level, title, message string) (NotificationRecord, error) {
	ts := time.Now().UTC().Format(time.RFC3339)
	res, err := d.sqlDB.Exec(
		`INSERT INTO notifications (level, title, message, read, created_at) VALUES (?, ?, ?, 0, ?)`,
		level, title, message, ts,
	)
	if err != nil {
		return NotificationRecord{}, err
	}
	id, _ := res.LastInsertId()

	if id%50 == 0 {
		_, _ = d.sqlDB.Exec(`DELETE FROM notifications WHERE read = 1 AND id <= (SELECT COALESCE(MAX(id), 0) - 200 FROM notifications)`)
	}

	return NotificationRecord{
		ID:        id,
		Level:     level,
		Title:     title,
		Message:   message,
		Read:      false,
		Timestamp: ts,
	}, nil
}

// ListUnreadNotifications returns all unread notifications oldest-first.
func (d *DB) ListUnreadNotifications() ([]NotificationRecord, error) {
	rows, err := d.sqlDB.Query(
		`SELECT id, level, title, message, read, created_at FROM notifications WHERE read = 0 ORDER BY id ASC LIMIT 25`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]NotificationRecord, 0)
	for rows.Next() {
		var r NotificationRecord
		var readInt int
		if err := rows.Scan(&r.ID, &r.Level, &r.Title, &r.Message, &readInt, &r.Timestamp); err != nil {
			return nil, err
		}
		r.Read = readInt != 0
		result = append(result, r)
	}
	return result, rows.Err()
}

// MarkNotificationsRead marks all unread notifications with id <= upToID (or all if upToID <= 0) as read.
func (d *DB) MarkNotificationsRead(upToID int64) error {
	if upToID <= 0 {
		_, err := d.sqlDB.Exec(`UPDATE notifications SET read = 1 WHERE read = 0`)
		return err
	}
	_, err := d.sqlDB.Exec(`UPDATE notifications SET read = 1 WHERE read = 0 AND id <= ?`, upToID)
	return err
}
