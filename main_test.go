package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReferenceWorkbook(t *testing.T) {
	path := `C:\Users\12der\Downloads\План завершений в августе 2026.xlsx`
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("эталонный Excel доступен только в локальном окружении разработчика")
	}
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

func TestMainRouteAcceptsBitrixPost(t *testing.T) {
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{store: NewStore(filepath.Join(t.TempDir(), "data.json")), tmpl: tmpl, basePath: basePath}
	req := httptest.NewRequest(http.MethodPost, appPath, strings.NewReader("DOMAIN=test.bitrix24.ru&PLACEMENT=DEFAULT"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST %s: status = %d, body = %s", appPath, recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "api.bitrix24.com/api/v1/") {
		t.Fatal("основной POST-маршрут не отрисовал Bitrix-версию")
	}
}

func TestDashboardUsesExternalBasePath(t *testing.T) {
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{store: NewStore(filepath.Join(t.TempDir(), "data.json")), tmpl: tmpl, basePath: "/dashboards/completion-plan"}
	recorder := httptest.NewRecorder()
	app.dashboard(recorder, httptest.NewRequest(http.MethodGet, "/completion-plan", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `href="/dashboards/completion-plan/static/style.css"`) {
		t.Fatal("CSS URL не содержит BASE_PATH")
	}
	if !strings.Contains(body, `action="/dashboards/completion-plan/upload"`) {
		t.Fatal("upload URL не содержит BASE_PATH")
	}
}

func TestTemplateContainsBitrixFieldCodes(t *testing.T) {
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{store: NewStore(filepath.Join(t.TempDir(), "data.db")), tmpl: tmpl, basePath: basePath}
	recorder := httptest.NewRecorder()
	app.bitrixApp(recorder, httptest.NewRequest(http.MethodPost, appPath, nil))
	body := recorder.Body.String()
	for _, code := range []string{"UF_CRM_1708427400582", "UF_CRM_1708427240655", "useOriginalUfNames"} {
		if !strings.Contains(body, code) {
			t.Fatalf("в Bitrix-шаблоне отсутствует %s", code)
		}
	}
}

func TestStoreKeepsBaseline(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "data.db"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := []ImportedRow{{Key: "1", Name: "Иванов", Hearing: "2026-08-10", Status: "В работе"}}
	if _, err := store.Import("2026-08", first); err != nil {
		t.Fatal(err)
	}
	second := []ImportedRow{{Key: "1", Name: "Иванов", Hearing: "2026-09-12", Status: "Завершен успешно"}}
	if _, err := store.Import("2026-08", second); err != nil {
		t.Fatal(err)
	}
	unchanged := store.Snapshot().Cases[0]
	if unchanged.CurrentHearing != "2026-08-10" || unchanged.CurrentStatus != "В работе" {
		t.Fatalf("повторный Excel изменил текущие данные: %+v", unchanged)
	}
	if _, err := store.UpdateCurrent([]CurrentUpdate{{Key: "1", Hearing: "2026-09-12", Status: "Завершен успешно"}}); err != nil {
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

func TestSyncEndpointUpdatesSQLite(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "dashboard.db"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Import("2026-08", []ImportedRow{{Key: "42", Name: "Контакт", Hearing: "2026-08-10", Status: "В работе"}}); err != nil {
		t.Fatal(err)
	}
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{store: store, tmpl: tmpl, basePath: basePath}
	payload, _ := json.Marshal([]CurrentUpdate{{Key: "42", Hearing: "2026-09-01", Status: "Завершен успешно"}})
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, appPath+"/sync", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("sync status = %d: %s", recorder.Code, recorder.Body.String())
	}
	item := store.Snapshot().Cases[0]
	if item.BaselineHearing != "2026-08-10" || item.CurrentHearing != "2026-09-01" || !item.IsPostponed() {
		t.Fatalf("неверные даты после sync: %+v", item)
	}
	if item.BaselineStatus != "В работе" || item.CurrentStatus != "Завершен успешно" {
		t.Fatalf("неверные статусы после sync: %+v", item)
	}
}
