package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestBitrixHandlerAcceptsPostAndAllowsFrame(t *testing.T) {
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{store: NewStore(filepath.Join(t.TempDir(), "data.json")), tmpl: tmpl}
	req := httptest.NewRequest(http.MethodPost, "/bitrix/app", strings.NewReader("DOMAIN=test.bitrix24.ru"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	app.bitrixApp(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "*.bitrix24.ru") {
		t.Fatal("Bitrix24 frame-ancestor отсутствует")
	}
	if !strings.Contains(recorder.Body.String(), "api.bitrix24.com/api/v1/") {
		t.Fatal("Bitrix24 JS SDK отсутствует")
	}
}

func TestDashboardUsesExternalBasePath(t *testing.T) {
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{store: NewStore(filepath.Join(t.TempDir(), "data.json")), tmpl: tmpl, basePath: "/dashboards"}
	recorder := httptest.NewRecorder()
	app.dashboard(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `href="/dashboards/static/style.css"`) {
		t.Fatal("CSS URL не содержит BASE_PATH")
	}
	if !strings.Contains(body, `action="/dashboards/upload"`) {
		t.Fatal("upload URL не содержит BASE_PATH")
	}
}

func TestNormalizeBasePath(t *testing.T) {
	for input, want := range map[string]string{"": "", "/": "", "dashboards": "/dashboards", "/dashboards/": "/dashboards"} {
		if got := normalizeBasePath(input); got != want {
			t.Fatalf("normalizeBasePath(%q) = %q, want %q", input, got, want)
		}
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
