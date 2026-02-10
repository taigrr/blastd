package db

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type Activity struct {
	ID               int64
	Project          string
	GitRemote        string
	StartedAt        time.Time
	EndedAt          time.Time
	Filetype         string
	LinesAdded       int
	LinesRemoved     int
	GitCommit        string
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

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}

	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS activities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT,
			git_remote TEXT,
			started_at DATETIME NOT NULL,
			ended_at DATETIME NOT NULL,
			filetype TEXT,
			lines_added INTEGER DEFAULT 0,
			lines_removed INTEGER DEFAULT 0,
			git_commit TEXT,
			actions_per_minute REAL,
			words_per_minute REAL,
			editor TEXT DEFAULT 'neovim',
			machine TEXT,
			synced BOOLEAN DEFAULT FALSE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_activities_synced ON activities(synced);
		CREATE INDEX IF NOT EXISTS idx_activities_started_at ON activities(started_at);
	`)
	return err
}

func (db *DB) InsertActivity(a *Activity) error {
	result, err := db.conn.Exec(`
		INSERT INTO activities (
			project, git_remote, started_at, ended_at, filetype,
			lines_added, lines_removed, git_commit,
			actions_per_minute, words_per_minute, editor, machine
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		a.Project, a.GitRemote, a.StartedAt, a.EndedAt, a.Filetype,
		a.LinesAdded, a.LinesRemoved, a.GitCommit,
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
		SELECT id, project, git_remote, started_at, ended_at, filetype,
			   lines_added, lines_removed, git_commit,
			   actions_per_minute, words_per_minute, editor, machine, created_at
		FROM activities
		WHERE synced = FALSE
		ORDER BY started_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []*Activity
	for rows.Next() {
		a := &Activity{}
		err := rows.Scan(
			&a.ID, &a.Project, &a.GitRemote, &a.StartedAt, &a.EndedAt, &a.Filetype,
			&a.LinesAdded, &a.LinesRemoved, &a.GitCommit,
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
	defer tx.Rollback()

	stmt, err := tx.Prepare("UPDATE activities SET synced = TRUE WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return err
		}
	}

	return tx.Commit()
}
