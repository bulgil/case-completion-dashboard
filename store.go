package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Case struct {
	Key             string `json:"key"`
	DealID          string `json:"deal_id"`
	Name            string `json:"name"`
	CaseNumber      string `json:"case_number"`
	Manager         string `json:"manager"`
	BaselineHearing string `json:"baseline_hearing"`
	CurrentHearing  string `json:"current_hearing"`
	BaselineStatus  string `json:"baseline_status"`
	CurrentStatus   string `json:"current_status"`
	BaselineStage   string `json:"baseline_stage"`
	CurrentStage    string `json:"current_stage"`
	DealStage       string `json:"deal_stage,omitempty"` // legacy JSON migration only
}

func (c Case) IsPostponed() bool {
	return c.BaselineHearing != "" && c.CurrentHearing != "" && c.BaselineHearing != c.CurrentHearing
}

type Snapshot struct {
	Period    string `json:"period"`
	UpdatedAt string `json:"updated_at"`
	Cases     []Case `json:"cases"`
}

type ImportResult struct{ Read, Added, Updated int }

type CurrentUpdate struct {
	Key     string `json:"id"`
	Status  string `json:"status"`
	Hearing string `json:"hearing"`
	Stage   string `json:"stage"`
}

type Store struct {
	path string
	db   *sql.DB
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return err
	}
	s.db = db
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}
	if _, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS cases (
			contact_id TEXT PRIMARY KEY,
			deal_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '', case_number TEXT NOT NULL DEFAULT '', manager TEXT NOT NULL DEFAULT '',
			baseline_hearing TEXT NOT NULL DEFAULT '', current_hearing TEXT NOT NULL DEFAULT '',
			baseline_status TEXT NOT NULL DEFAULT '', current_status TEXT NOT NULL DEFAULT '',
			deal_stage TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		return err
	}
	// Additive migrations keep existing installations and their fixed baseline intact.
	for _, statement := range []string{
		`ALTER TABLE cases ADD COLUMN baseline_stage TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cases ADD COLUMN current_stage TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cases ADD COLUMN deal_id TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = db.Exec(statement)
	}
	_, _ = db.Exec(`UPDATE cases SET baseline_stage=deal_stage WHERE baseline_stage=''`)
	_, _ = db.Exec(`UPDATE cases SET current_stage=deal_stage WHERE current_stage=''`)
	return s.migrateLegacyJSON()
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Snapshot() Snapshot {
	result := Snapshot{}
	if s.db == nil {
		return result
	}
	_ = s.db.QueryRow(`SELECT value FROM metadata WHERE key='period'`).Scan(&result.Period)
	_ = s.db.QueryRow(`SELECT value FROM metadata WHERE key='updated_at'`).Scan(&result.UpdatedAt)
	rows, err := s.db.Query(`SELECT contact_id,deal_id,name,case_number,manager,baseline_hearing,current_hearing,baseline_status,current_status,baseline_stage,current_stage FROM cases ORDER BY current_hearing,name`)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var item Case
		if rows.Scan(&item.Key, &item.DealID, &item.Name, &item.CaseNumber, &item.Manager, &item.BaselineHearing, &item.CurrentHearing, &item.BaselineStatus, &item.CurrentStatus, &item.BaselineStage, &item.CurrentStage) == nil {
			result.Cases = append(result.Cases, item)
		}
	}
	return result
}

func (s *Store) Import(period string, rows []ImportedRow) (ImportResult, error) {
	result := ImportResult{Read: len(rows)}
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	for _, row := range rows {
		if row.Key == "" {
			continue
		}
		var exists int
		err = tx.QueryRow(`SELECT 1 FROM cases WHERE contact_id=?`, row.Key).Scan(&exists)
		if err == nil {
			_, err = tx.Exec(`UPDATE cases SET deal_id=?,name=?,case_number=?,manager=? WHERE contact_id=?`, row.DealID, row.Name, row.CaseNumber, row.Manager, row.Key)
			if err != nil {
				return result, err
			}
			result.Updated++
			continue
		}
		if err != sql.ErrNoRows {
			return result, err
		}
		_, err = tx.Exec(`INSERT INTO cases(contact_id,deal_id,name,case_number,manager,baseline_hearing,current_hearing,baseline_status,current_status,deal_stage,baseline_stage,current_stage,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			row.Key, row.DealID, row.Name, row.CaseNumber, row.Manager, row.Hearing, row.Hearing, row.Status, row.Status, row.DealStage, row.DealStage, row.DealStage, time.Now().Format(time.RFC3339))
		if err != nil {
			return result, err
		}
		result.Added++
	}
	now := time.Now().Format("02.01.2006 15:04")
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('period',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, period); err != nil {
		return result, err
	}
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('updated_at',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, now); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Store) UpdateCurrent(updates []CurrentUpdate) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count := 0
	for _, update := range updates {
		if strings.TrimSpace(update.Key) == "" {
			continue
		}
		res, execErr := tx.Exec(`UPDATE cases SET current_status=?,current_hearing=?,current_stage=CASE WHEN ?='' THEN current_stage ELSE ? END,updated_at=? WHERE contact_id=?`, update.Status, update.Hearing, update.Stage, update.Stage, time.Now().Format(time.RFC3339), update.Key)
		if execErr != nil {
			return count, execErr
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			count++
		}
	}
	now := time.Now().Format("02.01.2006 15:04")
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('updated_at',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, now); err != nil {
		return count, err
	}
	return count, tx.Commit()
}

func (s *Store) migrateLegacyJSON() error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cases`).Scan(&count); err != nil || count > 0 {
		return err
	}
	legacyPath := strings.TrimSuffix(s.path, filepath.Ext(s.path)) + ".json"
	payload, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var legacy Snapshot
	if err = json.Unmarshal(payload, &legacy); err != nil {
		return fmt.Errorf("миграция JSON: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range legacy.Cases {
		baselineStage := item.BaselineStage
		if baselineStage == "" {
			baselineStage = item.DealStage
		}
		currentStage := item.CurrentStage
		if currentStage == "" {
			currentStage = baselineStage
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO cases(contact_id,deal_id,name,case_number,manager,baseline_hearing,current_hearing,baseline_status,current_status,deal_stage,baseline_stage,current_stage,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			item.Key, item.DealID, item.Name, item.CaseNumber, item.Manager, item.BaselineHearing, item.CurrentHearing, item.BaselineStatus, item.CurrentStatus, baselineStage, baselineStage, currentStage, time.Now().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	_, _ = tx.Exec(`INSERT INTO metadata(key,value) VALUES('period',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, legacy.Period)
	_, _ = tx.Exec(`INSERT INTO metadata(key,value) VALUES('updated_at',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, legacy.UpdatedAt)
	return tx.Commit()
}
