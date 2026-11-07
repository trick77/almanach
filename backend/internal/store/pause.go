package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Pause is the queue's pause: since when. Who paused is the log's.
type Pause struct {
	At time.Time
}

// PauseWorker pauses the queue; a pause on already stays as it was, its
// time the first's.
func (s *Store) PauseWorker(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO worker_pause (id, paused_at) VALUES (1, ?) ON CONFLICT (id) DO NOTHING`, nowStamp())
	return err
}

// ResumeWorker lifts the pause; none on is no error.
func (s *Store) ResumeWorker(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM worker_pause`)
	return err
}

// WorkerPause is the pause on, nil when none is.
func (s *Store) WorkerPause(ctx context.Context) (*Pause, error) {
	var p Pause
	err := s.db.QueryRowContext(ctx, `SELECT paused_at FROM worker_pause WHERE id = 1`).Scan(timeAt(&p.At))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
