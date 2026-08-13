package main

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/* static/*
var assets embed.FS

type application struct {
	tmpl     *template.Template
	basePath string
}

type dashboardData struct {
	Title, Period, PeriodInput, UpdatedAt, Message, BasePath string
	Total, Completed, Postponed                              int
	Rows                                                     []Case
	Embedded, CanSync, AutoSync                              bool
}

const (
	listenAddr = ":1987"
	basePath   = "/dashboards/completion-plan"
	appPath    = "/completion-plan"
)

func main() {
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{"statusClass": statusClass, "dateRU": dateRU}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{tmpl: tmpl, basePath: basePath}
	server := &http.Server{Addr: listenAddr, Handler: logRequests(app.routes()), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Дашборд запущен: http://localhost%s%s", listenAddr, appPath)
	log.Fatal(server.ListenAndServe())
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+appPath, a.dashboard)
	mux.HandleFunc("POST "+appPath, a.bitrixApp)
	mux.HandleFunc("GET "+appPath+"/bitrix/app", a.bitrixApp)
	mux.HandleFunc("POST "+appPath+"/bitrix/app", a.bitrixApp)
	mux.HandleFunc("POST "+appPath+"/upload", a.upload)
	mux.HandleFunc("POST "+appPath+"/export", a.exportExcel)
	mux.HandleFunc("GET "+appPath+"/healthz", healthcheck)
	mux.Handle("GET "+appPath+"/static/", http.StripPrefix(appPath, http.FileServer(http.FS(assets))))
	return mux
}

func (a *application) dashboard(w http.ResponseWriter, r *http.Request) {
	a.render(w, dashboardData{PeriodInput: "2026-08", BasePath: a.basePath})
}
func (a *application) bitrixApp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "frame-ancestors https://*.bitrix24.ru https://*.bitrix24.com https://*.bitrix24.eu https://*.bitrix24.de https://*.bitrix24.by https://*.bitrix24.kz 'self'")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	a.render(w, dashboardData{PeriodInput: "2026-08", BasePath: a.basePath, Embedded: true})
}

func (a *application) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		a.renderError(w, r, "Файл слишком большой или форма повреждена")
		return
	}
	period := strings.TrimSpace(r.FormValue("period"))
	if _, err := time.Parse("2006-01", period); err != nil {
		a.renderError(w, r, "Выберите корректный месяц плана")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		a.renderError(w, r, "Выберите Excel-файл")
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		a.renderError(w, r, "Поддерживаются файлы .xlsx")
		return
	}
	tmp, err := os.CreateTemp("", "dashboard-*.xlsx")
	if err != nil {
		a.renderError(w, r, "Не удалось принять файл")
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = io.Copy(tmp, file); err != nil || tmp.Close() != nil {
		a.renderError(w, r, "Не удалось сохранить файл")
		return
	}
	rows, err := ParseWorkbook(name)
	if err != nil {
		a.renderError(w, r, "Ошибка чтения Excel: "+err.Error())
		return
	}
	cases := make([]Case, 0, len(rows))
	for _, row := range rows {
		cases = append(cases, Case{Key: row.Key, DealID: row.DealID, Name: row.Name, CaseNumber: row.CaseNumber, Manager: row.Manager, BaselineHearing: row.Hearing, CurrentHearing: row.Hearing, BaselineStatus: row.Status, CurrentStatus: row.Status, BaselineStage: row.DealStage, CurrentStage: row.DealStage})
	}
	embedded := r.FormValue("embedded") == "1"
	a.render(w, dashboardData{PeriodInput: period, Rows: cases, Embedded: embedded, CanSync: embedded && len(cases) > 0, AutoSync: embedded && len(cases) > 0, BasePath: a.basePath, Message: fmt.Sprintf("Загружено видимых строк: %d", len(cases))})
}

func (a *application) renderError(w http.ResponseWriter, r *http.Request, message string) {
	a.render(w, dashboardData{PeriodInput: r.FormValue("period"), Embedded: r.FormValue("embedded") == "1", BasePath: a.basePath, Message: message})
}
func (a *application) render(w http.ResponseWriter, data dashboardData) {
	if data.PeriodInput == "" {
		data.PeriodInput = "2026-08"
	}
	data.Title = titleForPeriod(data.PeriodInput)
	data.Period = humanPeriod(data.PeriodInput)
	data.Total = len(data.Rows)
	for _, item := range data.Rows {
		if isCompleted(item.CurrentStatus) {
			data.Completed++
		}
		if item.IsPostponed() {
			data.Postponed++
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, "Ошибка отображения", 500)
	}
}

func healthcheck(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}
func statusClass(status string) string {
	if isCompleted(status) {
		return "success"
	}
	if strings.TrimSpace(status) == "" {
		return "muted"
	}
	return "active"
}
func isCompleted(status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	normalized = strings.ReplaceAll(normalized, "ё", "е")
	return normalized == "завершен успешно"
}
func humanPeriod(period string) string {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return period
	}
	months := []string{"январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}
	return months[int(t.Month())-1] + " " + strconv.Itoa(t.Year())
}
func titleForPeriod(period string) string {
	return "План по завершению — " + humanPeriod(period)
}
func dateRU(value string) string {
	if value == "" {
		return "—"
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.Format("02.01.2006")
	}
	return value
}
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(started).Round(time.Millisecond))
	})
}
