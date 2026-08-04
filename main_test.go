package main

import (
	"path/filepath"
	"testing"
)

func TestParseReferenceWorkbook(t *testing.T) {
	path := `C:\Users\12der\Downloads\План завершений в августе 2026.xlsx`
	rows, err := ParseWorkbook(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2936 {
		t.Fatalf("получено %d строк, ожидалось 2936", len(rows))
	}
	if rows[0].Name != "Колоколова Гульнара Гайзулловна" {
		t.Fatalf("неожиданная первая строка: %+v", rows[0])
	}
	if rows[0].Hearing != "2026-08-18" {
		t.Fatalf("неожиданная дата: %s", rows[0].Hearing)
	}
}

func TestStoreKeepsBaseline(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "data.json"))
	first := []ImportedRow{{Key: "1", Name: "Иванов", Hearing: "2026-08-10", Status: "В работе"}}
	if _, err := store.Import("2026-08", first); err != nil {
		t.Fatal(err)
	}
	second := []ImportedRow{{Key: "1", Name: "Иванов", Hearing: "2026-09-12", Status: "Завершен успешно"}}
	if _, err := store.Import("2026-08", second); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Cases[0]
	if got.BaselineHearing != "2026-08-10" || got.CurrentHearing != "2026-09-12" || !got.IsPostponed() {
		t.Fatalf("неверное сравнение: %+v", got)
	}
	if got.BaselineStatus != "В работе" || got.CurrentStatus != "Завершен успешно" {
		t.Fatalf("неверные статусы: %+v", got)
	}
}
