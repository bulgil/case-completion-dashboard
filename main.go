package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/* static/*
var assets embed.FS

type application struct {
	store    *Store
	tracker  *BitrixTracker
	tmpl     *template.Template
	basePath string
}

type dashboardData struct {
	Title       string
	Period      string
	PeriodInput string
	UpdatedAt   string
	Total       int
	Completed   int
	Postponed   int
	Rows        []Case
	Message     string
	Embedded    bool
	BasePath    string
	CanSync     bool
}

const (
	listenAddr = ":1987"
	basePath   = "/dashboards/completion-plan"
	appPath    = "/completion-plan"
)

func main() {
	dataFile := env("DATA_FILE", filepath.Join("data", "dashboard.db"))
	tmpl := template.Must(template.New("dashboard.html").Funcs(template.FuncMap{
		"statusClass": statusClass,
		"dateRU":      dateRU,
	}).ParseFS(assets, "templates/dashboard.html"))
	app := &application{store: NewStore(dataFile), tmpl: tmpl, basePath: basePath}
	if err := app.store.Load(); err != nil {
		log.Fatalf("загрузка данных: %v", err)
	}
	defer app.store.Close()
	app.tracker = NewBitrixTracker(app.store, os.Getenv("BITRIX_WEBHOOK_URL"))
	go app.tracker.Run(context.Background())

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
	mux.HandleFunc("POST "+appPath+"/sync", a.syncCurrent)
	mux.HandleFunc("POST "+appPath+"/refresh", a.refreshBitrix)
	mux.HandleFunc("GET "+appPath+"/healthz", healthcheck)
	mux.Handle("GET "+appPath+"/static/", http.StripPrefix(appPath, http.FileServer(http.FS(assets))))
	return mux
}

func (a *application) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("embedded") == "1" {
		a.bitrixApp(w, r)
		return
	}
	a.renderDashboard(w, r, false)
}

func (a *application) bitrixApp(w http.ResponseWriter, r *http.Request) {
	// Bitrix24 opens application handlers in an iframe and sends context as POST.
	// We do not persist OAuth tokens because the dashboard currently needs no REST calls.
	w.Header().Set("Content-Security-Policy", "frame-ancestors https://*.bitrix24.ru https://*.bitrix24.com https://*.bitrix24.eu https://*.bitrix24.de https://*.bitrix24.by https://*.bitrix24.kz 'self'")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	a.renderDashboard(w, r, true)
}

func (a *application) renderDashboard(w http.ResponseWriter, r *http.Request, embedded bool) {
	snapshot := a.store.Snapshot()
	period := snapshot.Period
	if period == "" {
		period = "2026-08"
	}
	data := dashboardData{
		Title:       titleForPeriod(period),
		Period:      humanPeriod(period),
		PeriodInput: period,
		UpdatedAt:   snapshot.UpdatedAt,
		Rows:        snapshot.Cases,
		Total:       len(snapshot.Cases),
		Embedded:    embedded,
		BasePath:    a.basePath,
		CanSync:     a.tracker != nil && a.tracker.Enabled() && len(snapshot.Cases) > 0,
	}
	for _, item := range snapshot.Cases {
		if isCompleted(item.CurrentStatus) {
			data.Completed++
		}
		if item.IsPostponed() {
			data.Postponed++
		}
	}
	data.Message = r.URL.Query().Get("message")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, "Ошибка отображения", http.StatusInternalServerError)
	}
}

func (a *application) refreshBitrix(w http.ResponseWriter, r *http.Request) {
	if a.tracker == nil || !a.tracker.Enabled() {
		a.redirectMessage(w, r, "Для фонового отслеживания настройте BITRIX_WEBHOOK_URL")
		return
	}
	count, err := a.tracker.Sync(r.Context())
	if err != nil {
		a.redirectMessage(w, r, "Ошибка обновления Bitrix24: "+err.Error())
		return
	}
	a.redirectMessage(w, r, fmt.Sprintf("Из Bitrix24 обновлено записей: %d", count))
}

func (a *application) syncCurrent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var updates []CurrentUpdate
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&updates); err != nil {
		http.Error(w, "Некорректные данные синхронизации", http.StatusBadRequest)
		return
	}
	count, err := a.store.UpdateCurrent(updates)
	if err != nil {
		http.Error(w, "Не удалось сохранить данные Bitrix24", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]int{"updated": count})
}

func healthcheck(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (a *application) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		a.redirectMessage(w, r, "Файл слишком большой или форма повреждена")
		return
	}
	period := strings.TrimSpace(r.FormValue("period"))
	if _, err := time.Parse("2006-01", period); err != nil {
		a.redirectMessage(w, r, "Выберите корректный месяц плана")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		a.redirectMessage(w, r, "Выберите Excel-файл")
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		a.redirectMessage(w, r, "Поддерживаются файлы .xlsx")
		return
	}
	tmp, err := os.CreateTemp("", "dashboard-*.xlsx")
	if err != nil {
		a.redirectMessage(w, r, "Не удалось принять файл")
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = io.Copy(tmp, file); err != nil || tmp.Close() != nil {
		a.redirectMessage(w, r, "Не удалось сохранить загруженный файл")
		return
	}

	rows, err := ParseWorkbook(tmpName)
	if err != nil {
		a.redirectMessage(w, r, "Ошибка чтения Excel: "+err.Error())
		return
	}
	result, err := a.store.Import(period, rows)
	if err != nil {
		a.redirectMessage(w, r, "Ошибка сохранения: "+err.Error())
		return
	}
	if a.tracker != nil && a.tracker.Enabled() && result.Added > 0 {
		go func() {
			if _, syncErr := a.tracker.Sync(context.Background()); syncErr != nil {
				log.Printf("обновление Bitrix24 после загрузки: %v", syncErr)
			}
		}()
	}
	message := fmt.Sprintf("Загружено: %d; добавлено в план: %d; обновлено: %d", result.Read, result.Added, result.Updated)
	a.redirectMessage(w, r, message)
}

func (a *application) redirectMessage(w http.ResponseWriter, r *http.Request, message string) {
	target := a.basePath
	separator := "?"
	if r.FormValue("embedded") == "1" || (strings.HasPrefix(r.Referer(), "https://") && strings.Contains(r.Referer(), a.basePath)) {
		target = a.basePath + "?embedded=1&sync=1"
		separator = "&"
	}
	http.Redirect(w, r, target+separator+"message="+urlQueryEscape(message), http.StatusSeeOther)
}

func urlQueryEscape(value string) string {
	result := ""
	for _, b := range []byte(value) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || strings.ContainsRune("-_.~", rune(b)) {
			result += string(b)
		} else {
			result += "%" + strings.ToUpper(strconv.FormatInt(int64(b), 16))
		}
	}
	return result
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

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
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
