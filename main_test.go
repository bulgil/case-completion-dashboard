package main

import (
	"archive/zip"
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testApp(t *testing.T) *application {
	t.Helper()
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	return &application{tmpl: tmpl, basePath: basePath}
}

func TestBitrixHandlerAcceptsPost(t *testing.T) {
	recorder := httptest.NewRecorder()
	testApp(t).routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, appPath, strings.NewReader("DOMAIN=test.bitrix24.ru")))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "api.bitrix24.com/api/v1/") {
		t.Fatal("Bitrix SDK отсутствует")
	}
}

func TestDashboardIsStateless(t *testing.T) {
	recorder := httptest.NewRecorder()
	testApp(t).dashboard(recorder, httptest.NewRequest(http.MethodGet, appPath, nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `action="/dashboards/completion-plan/upload"`) {
		t.Fatal("неверный upload URL")
	}
	if strings.Contains(body, "Обновлено из Bitrix24:") {
		t.Fatal("пустой дашборд содержит сохранённые данные")
	}
}

func TestRenderUploadedRowsEnablesSync(t *testing.T) {
	app := testApp(t)
	recorder := httptest.NewRecorder()
	app.render(recorder, dashboardData{PeriodInput: "2026-08", BasePath: basePath, Embedded: true, CanSync: true, AutoSync: true, Rows: []Case{{Key: "42", DealID: "84", Name: "Иванов", BaselineStage: "Суд", CurrentStage: "Суд"}}})
	body := recorder.Body.String()
	for _, expected := range []string{`data-contact-id="42"`, `data-deal-id="84"`, `syncWithBitrix(syncButton)`, `Экспорт в Excel`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("нет %q", expected)
		}
	}
}

func TestTemplateUsesWorkingBitrixUniversalMethods(t *testing.T) {
	app := testApp(t)
	recorder := httptest.NewRecorder()
	app.render(recorder, dashboardData{PeriodInput: "2026-08", BasePath: basePath, Embedded: true, CanSync: true, Rows: []Case{{Key: "42", DealID: "84"}}})
	body := recorder.Body.String()
	for _, expected := range []string{"crm.item.fields", "crm.item.get", "entityTypeId: 3", "entityTypeId: 2", "crm.status.list", "STATUS_ID"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("нет %q", expected)
		}
	}
}

func TestEmbeddedDashboardRequestsDesktopWidth(t *testing.T) {
	app := testApp(t)
	recorder := httptest.NewRecorder()
	app.render(recorder, dashboardData{PeriodInput: "2026-08", BasePath: basePath, Embedded: true})
	body := recorder.Body.String()
	if !strings.Contains(body, "Math.max(1180") || !strings.Contains(body, "BX24.resizeWindow(width, height)") {
		t.Fatal("iframe не запрашивает рабочую ширину дашборда")
	}
}

func TestDashboardShowsBaselineAndCurrentContactStatus(t *testing.T) {
	app := testApp(t)
	recorder := httptest.NewRecorder()
	app.render(recorder, dashboardData{PeriodInput: "2026-08", BasePath: basePath, Rows: []Case{{Key: "42", BaselineStatus: "В работе", CurrentStatus: "Завершен успешно"}}})
	body := recorder.Body.String()
	for _, expected := range []string{"Статус контакта на начало", "Статус контакта текущий", `data-baseline-status="В работе"`, "Завершен успешно"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("нет %q", expected)
		}
	}
}

func TestExportExcel(t *testing.T) {
	payload := `[{"key":"42","name":"Иванов","baseline_stage":"Суд","current_stage":"Завершена"}]`
	recorder := httptest.NewRecorder()
	testApp(t).exportExcel(recorder, httptest.NewRequest(http.MethodPost, appPath+"/export", strings.NewReader(payload)))
	reader, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	if err != nil {
		t.Fatalf("invalid xlsx: %v", err)
	}
	if len(reader.File) < 5 {
		t.Fatalf("xlsx parts=%d", len(reader.File))
	}
}

func TestCompletedNormalization(t *testing.T) {
	if !isCompleted(" Завершён успешно ") {
		t.Fatal("статус не распознан")
	}
}
