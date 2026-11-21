package handler // HTTP-слой: маршруты, страницы, общие помощники

import (
	"bytes"         // буфер рендера
	"encoding/json" // JSON-ответы
	"html/template" // шаблоны с автоэкранированием
	"log"           // журнал
	"net/http"      // сервер
	"os"            // папки
	"path/filepath" // пути
	"strings"       // префиксы API

	"student-activity/internal/caldavbackend"
	"student-activity/internal/config"
	"student-activity/internal/icalgen"
	"student-activity/internal/model"
	"student-activity/internal/storage"

	"github.com/emersion/go-webdav/caldav"
)

type Handler struct {
	store   *storage.Storage
	cfg     config.Config
	tpl     *template.Template
	gen     *icalgen.Generator
	caldavB *caldavbackend.Backend
}

func New(store *storage.Storage, cfg config.Config, gen *icalgen.Generator, cb *caldavbackend.Backend) (*Handler, error) {
	tpl, err := template.ParseGlob(filepath.Join("web", "templates", "*.html")) // все шаблоны разом
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil { // папка архива CSV
		return nil, err
	}
	return &Handler{store: store, cfg: cfg, tpl: tpl, gen: gen, caldavB: cb}, nil
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Публичное: вход, регистрация, статика, iCal-лента по хешу.
	mux.HandleFunc("GET /{$}", h.root) // первый экран: редирект по роли или на /login
	mux.HandleFunc("GET /login", h.pageLogin)
	mux.HandleFunc("POST /api/login", h.apiLogin)
	mux.HandleFunc("GET /register", h.pageRegister)
	mux.HandleFunc("POST /api/register", h.apiRegister)
	mux.HandleFunc("POST /api/logout", h.apiLogout)
	mux.HandleFunc("GET /ical/{hash}", h.icalFeed) // секрет-хеш = доступ
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join("web", "static")))))

	// Студент.
	mux.Handle("GET /student", h.requireRoles(http.HandlerFunc(h.pageStudent), model.RoleStudent))
	mux.Handle("POST /api/student/profile", h.requireRoles(http.HandlerFunc(h.apiStudentProfile), model.RoleStudent))

	// Преподаватель.
	mux.Handle("GET /teacher", h.requireRoles(http.HandlerFunc(h.pageTeacher), model.RoleTeacher))
	mux.Handle("POST /api/teacher/requests", h.requireRoles(http.HandlerFunc(h.apiCreateRequest), model.RoleTeacher))
	mux.Handle("GET /api/rooms/free", h.requireRoles(http.HandlerFunc(h.apiFreeRooms), model.RoleTeacher, model.RoleAdmin))

	// Админ.
	mux.Handle("GET /admin", h.requireRoles(http.HandlerFunc(h.pageAdmin), model.RoleAdmin))
	mux.Handle("POST /api/admin/requests/{id}/decide", h.requireRoles(http.HandlerFunc(h.apiDecide), model.RoleAdmin))
	mux.Handle("GET /admin/upload", h.requireRoles(http.HandlerFunc(h.pageUpload), model.RoleAdmin))
	mux.Handle("POST /admin/upload", h.requireRoles(http.HandlerFunc(h.apiUploadCSV), model.RoleAdmin))
	mux.Handle("GET /admin/teachers", h.requireRoles(http.HandlerFunc(h.pageTeachers), model.RoleAdmin))
	mux.Handle("POST /api/admin/teachers", h.requireRoles(http.HandlerFunc(h.apiCreateTeacher), model.RoleAdmin))

	// CalDAV: ВСЕ методы (PROPFIND/REPORT/...), поэтому паттерн без метода.
	// Basic-auth: логин + пароль ИЛИ логин + ical-хеш (привязка пользователя к календарю).
	mux.Handle("/caldav/", h.caldavBasicAuth(&caldav.Handler{Backend: h.caldavB, Prefix: "/caldav/"}))
}

// root — «сначала авторизация»: без сессии → /login, с сессией → домашняя страница роли.
func (h *Handler) root(w http.ResponseWriter, r *http.Request) {
	u, err := h.currentUser(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, homeFor(u.Role), http.StatusSeeOther)
}

func homeFor(role string) string { // домашняя страница по роли
	switch role {
	case model.RoleAdmin:
		return "/admin"
	case model.RoleTeacher:
		return "/teacher"
	default:
		return "/student"
	}
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) { // шаблон → буфер → ответ
	var buf bytes.Buffer
	if err := h.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("шаблон %s: %v", name, err)
		http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func writeJSON(w http.ResponseWriter, status int, v any) { // единый JSON-ответ
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json: %v", err)
	}
}

func errJSON(w http.ResponseWriter, status int, msg string) { // короткая ошибка
	writeJSON(w, status, map[string]string{"error": msg})
}

// appData превращает любые данные в безопасный JSON для <script>window.APP_DATA=...</script>.
func appDataJSON(v any) string {
	b, err := json.Marshal(v) // структура → JSON
	if err != nil {
		log.Printf("appData: %v", err)
		return "{}" // запасной вариант: пустой объект, чтобы JS не упал
	}
	return string(b)
}

// catalogs — группы и доп. активности для селектов (БД + справочники).
func (h *Handler) catalogs() (groups, extras []string) {
	dbGroups, err := h.store.Groups()
	if err != nil {
		log.Printf("группы: %v", err)
	}
	if len(dbGroups) == 0 {
		dbGroups = model.FallbackGroups // пока CSV не загружен — список из вашего index.js
	}
	seen := make(map[string]bool)
	for _, a := range model.Activities { // сначала справочник активностей
		extras = append(extras, a)
		seen[a] = true
	}
	dbExtras, err := h.store.ExtraGroups() // потом нечисловые «группы» из расписания
	if err != nil {
		log.Printf("доп. группы: %v", err)
	}
	for _, e := range dbExtras {
		if !seen[e] {
			extras = append(extras, e)
			seen[e] = true
		}
	}
	return dbGroups, extras
}

// links — ссылки календаря студента (лента + CalDAV).
func (h *Handler) links(u model.User) map[string]string {
	feed := "http://" + h.cfg.Host + "/ical/" + u.IcalHash + ".ics"
	return map[string]string{
		"ical":       feed,
		"webcal":     "webcal://" + h.cfg.Host + "/ical/" + u.IcalHash + ".ics", // подписка в 1 клик
		"caldav":     "http://" + h.cfg.Host + "/caldav/",
		"caldavUser": u.Username,
		"caldavPass": u.IcalHash, // хеш = пароль для CalDAV-клиента
	}
}

var _ = strings.HasPrefix // используется в auth.go (оставляем импорт общим файлом пакета)
