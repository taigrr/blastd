package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

type Activity struct {
	ID               int64
	ClientID         string
	Project          string
	GitRemote        string
	StartedAt        time.Time
	EndedAt          time.Time
	Filename         string
	Filetype         string
	LinesAdded       int
	LinesRemoved     int
	GitBranch        string
	ActionsPerMinute float64
	WordsPerMinute   float64
	Editor           string
	Machine          string
	Synced           bool
	CreatedAt        time.Time
}

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	goose.SetBaseFS(FS)

	if err := goose.SetDialect("sqlite3"); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to set dialect: %w (close db: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("failed to set dialect: %w", err)
	}

	if err := goose.Up(conn, "migrations"); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to apply migrations: %w (close db: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) InsertActivity(a *Activity) error {
	if a.ClientID == "" {
		a.ClientID = uuid.NewString()
	}

	result, err := db.conn.Exec(`
		INSERT INTO activities (
			client_id, project, git_remote, started_at, ended_at, filename, filetype,
			lines_added, lines_removed, git_branch,
			actions_per_minute, words_per_minute, editor, machine
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		a.ClientID, a.Project, a.GitRemote, a.StartedAt, a.EndedAt, a.Filename, a.Filetype,
		a.LinesAdded, a.LinesRemoved, a.GitBranch,
		a.ActionsPerMinute, a.WordsPerMinute, a.Editor, a.Machine,
	)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = id
	return nil
}

func (db *DB) GetUnsyncedActivities(limit int) ([]*Activity, error) {
	rows, err := db.conn.Query(`
		SELECT id, client_id,
			   COALESCE(project, ''), COALESCE(git_remote, ''),
			   started_at, ended_at,
			   COALESCE(filename, ''), COALESCE(filetype, ''),
			   COALESCE(lines_added, 0), COALESCE(lines_removed, 0),
			   COALESCE(git_branch, ''),
			   COALESCE(actions_per_minute, 0), COALESCE(words_per_minute, 0),
			   COALESCE(editor, 'neovim'), COALESCE(machine, ''), created_at
		FROM activities
		WHERE synced = FALSE
		ORDER BY started_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = closeErr
		}
	}()

	var activities []*Activity
	for rows.Next() {
		a := &Activity{}
		err := rows.Scan(
			&a.ID, &a.ClientID, &a.Project, &a.GitRemote, &a.StartedAt, &a.EndedAt, &a.Filename, &a.Filetype,
			&a.LinesAdded, &a.LinesRemoved, &a.GitBranch,
			&a.ActionsPerMinute, &a.WordsPerMinute, &a.Editor, &a.Machine, &a.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		activities = append(activities, a)
	}
	return activities, rows.Err()
}

func (db *DB) MarkSynced(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && rollbackErr != sql.ErrTxDone {
			err = rollbackErr
		}
	}()

	stmt, err := tx.Prepare("UPDATE activities SET synced = TRUE WHERE id = ?")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return err
		}
	}

	return tx.Commit()
}
