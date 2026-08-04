package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Case struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	CaseNumber      string `json:"case_number"`
	Manager         string `json:"manager"`
	BaselineHearing string `json:"baseline_hearing"`
	CurrentHearing  string `json:"current_hearing"`
	BaselineStatus  string `json:"baseline_status"`
	CurrentStatus   string `json:"current_status"`
	DealStage       string `json:"deal_stage"`
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

type Store struct {
	mu   sync.RWMutex
	path string
	data Snapshot
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(content, &s.data)
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyData := s.data
	copyData.Cases = append([]Case(nil), s.data.Cases...)
	return copyData
}

func (s *Store) Import(period string, rows []ImportedRow) (ImportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ImportResult{Read: len(rows)}
	if s.data.Period != "" && s.data.Period != period {
		s.data = Snapshot{}
	}
	byKey := make(map[string]int, len(s.data.Cases))
	for i := range s.data.Cases {
		byKey[s.data.Cases[i].Key] = i
	}
	for _, row := range rows {
		if row.Key == "" {
			continue
		}
		if idx, ok := byKey[row.Key]; ok {
			item := &s.data.Cases[idx]
			item.Name, item.CaseNumber, item.Manager = row.Name, row.CaseNumber, row.Manager
			item.CurrentHearing, item.CurrentStatus, item.DealStage = row.Hearing, row.Status, row.DealStage
			result.Updated++
			continue
		}
		if !dateInPeriod(row.Hearing, period) {
			continue
		}
		item := Case{Key: row.Key, Name: row.Name, CaseNumber: row.CaseNumber, Manager: row.Manager,
			BaselineHearing: row.Hearing, CurrentHearing: row.Hearing,
			BaselineStatus: row.Status, CurrentStatus: row.Status, DealStage: row.DealStage}
		byKey[item.Key] = len(s.data.Cases)
		s.data.Cases = append(s.data.Cases, item)
		result.Added++
	}
	s.data.Period = period
	s.data.UpdatedAt = time.Now().Format("02.01.2006 15:04")
	sort.SliceStable(s.data.Cases, func(i, j int) bool {
		if s.data.Cases[i].CurrentHearing == s.data.Cases[j].CurrentHearing {
			return s.data.Cases[i].Name < s.data.Cases[j].Name
		}
		return s.data.Cases[i].CurrentHearing < s.data.Cases[j].CurrentHearing
	})
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return result, err
	}
	payload, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return result, err
	}
	tmp := s.path + ".tmp"
	if err = os.WriteFile(tmp, payload, 0644); err != nil {
		return result, err
	}
	if err = os.Rename(tmp, s.path); err != nil {
		return result, fmt.Errorf("замена файла данных: %w", err)
	}
	return result, nil
}

func dateInPeriod(value, period string) bool { return strings.HasPrefix(value, period+"-") }
