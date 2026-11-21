// Пакет handler — «переводчик» между HTTP и storage:
// разбирает запросы, проверяет данные, вызывает базу, формирует ответы.
package handler

import (
	"bytes"          // буфер в памяти: сначала рендерим шаблон сюда
	"encoding/json"  // ответы API в формате JSON
	"html/template"  // шаблоны с АВТОЭКРАНИРОВАНИЕМ: защита от XSS «из коробки»
	"log"            // журнал внутренних ошибок
	"net/http"       // сервер, запросы, ответы
	"os"             // создаём папку загрузок
	"path/filepath"  // склейка путей (кроссплатформенно)

	"student-activity/internal/config"  // настройки (папка загрузок и т.д.)
	"student-activity/internal/storage" // доступ к базе через методы
)

// Handler хранит зависимости всех обработчиков: базу, настройки, шаблоны.
// Приём «внедрение зависимостей»: обработчики не создают базу сами — получают готовую.
type Handler struct {
	store     *storage.Storage   // указатель: база одна на всё приложение
	cfg       config.Config      // копия настроек — структура маленькая
	templates *template.Template // разобранные шаблоны (парсим один раз при старте)
}

// New собирает Handler: парсит ВСЕ шаблоны и готовит папку загрузок.
func New(store *storage.Storage, cfg config.Config) (*Handler, error) {
	// ParseGlob читает все *.html. Путь относительный → запускать из КОРНЯ проекта.
	tpl, err := template.ParseGlob(filepath.Join("web", "templates", "*.html"))
	if err != nil {
		return nil, err // например, опечатка в шаблоне — узнаем сразу при старте, а не в бою
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil { // папка для файлов студентов
		return nil, err
	}
	return &Handler{store: store, cfg: cfg, templates: tpl}, nil // всё готово
}

// RegisterRoutes описывает карту сайта. Синтаксис "МЕТОД /путь" — стандарт Go 1.22+:
// роутер сам вернёт 405, если метод не тот, и разрулит пересечения маршрутов.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// --- Страницы (отдают HTML) ---
	mux.HandleFunc("GET /{$}", h.pageIndex)   // {$} = ТОЧНО корень "/", а не «всё подряд»
	mux.HandleFunc("GET /login", h.pageLogin) // страница входа
	mux.HandleFunc("GET /upload", h.pageUpload) // страница загрузки документа
	// Админку и её API закрываем(requireAuth): без живой сессии — редирект на /login.
	mux.Handle("GET /admin", h.requireAuth(http.HandlerFunc(h.pageAdmin)))

	// --- API (принимают JSON/файлы, отвечают JSON) ---
	mux.HandleFunc("POST /api/submissions", h.apiCreateSubmission) // отправка формы
	mux.HandleFunc("POST /api/upload", h.apiUpload)                // загрузка файла
	mux.HandleFunc("POST /api/login", h.apiLogin)                  // вход
	mux.HandleFunc("POST /api/logout", h.apiLogout)                // выход (всегда доступен)
	// {id} — сегмент-параметр; читается через r.PathValue("id"). Только для админа.
	mux.Handle("DELETE /api/submissions/{id}", h.requireAuth(http.HandlerFunc(h.apiDeleteSubmission)))

	// --- Статика: CSS/JS фронтенда ---
	// FileServer раздаёт файлы из папки; StripPrefix убирает "/static/" из пути:
	// запрос /static/css/index.css → файл web/static/css/index.css.
	staticFS := http.FileServer(http.Dir(filepath.Join("web", "static")))
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticFS))

	// --- Загруженные документы: ТОЛЬКО для админа ---
	// Иначе любой человек по ссылке читал бы чужие справки. Имена файлов случайные,
	// FileServer сам очищает путь от "../" — выхода из папки не будет.
	uploadsFS := http.FileServer(http.Dir(h.cfg.UploadDir))
	mux.Handle("GET /uploads/", h.requireAuth(http.StripPrefix("/uploads/", uploadsFS)))
}

// render выполняет шаблон в БУФЕР, и только потом отправляет клиенту.
// Если шаблон сломается на середине — клиент получит целую 500-ю страницу,
// а не обрывок HTML (поэтому не пишем шаблон прямо в w).
func (h *Handler) render(w http.ResponseWriter, name string, data any) { // any = любой тип
	var buf bytes.Buffer // bytes.Buffer реализует io.Writer — шаблон пишет сюда
	if err := h.templates.ExecuteTemplate(&buf, name, data); err != nil { // имя = имя файла
		log.Printf("шаблон %s: %v", name, err) // подробности — в журнал, не клиенту
		http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8") // говорим браузеру: это HTML, UTF-8
	buf.WriteTo(w) // одним махом отдаём готовую страницу
}

// writeJSON — единый способ отвечать API: статус + JSON-тело.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8") // тип ответа — JSON
	w.WriteHeader(status) // отправляем код (200, 400, 401, ...)
	if err := json.NewEncoder(w).Encode(v); err != nil { // сериализуем прямо в соединение
		log.Printf("запись JSON: %v", err) // клиент уже мог отвалиться — просто фиксируем
	}
}

// ---------- Обработчики страниц ----------

// pageIndex — главная форма. Данные не нужны: списки строит фронтенд (index.js).
func (h *Handler) pageIndex(w http.ResponseWriter, r *http.Request) {
	h.render(w, "index.html", nil) // nil = шаблон без переменных
}

// pageUpload — страница прикрепления документа (номер заявки читает JS из ?id=).
func (h *Handler) pageUpload(w http.ResponseWriter, r *http.Request) {
	h.render(w, "upload.html", nil)
}

// pageLogin — форма входа. Уже вошедшего незачем спрашивать пароль — сразу в админку.
func (h *Handler) pageLogin(w http.ResponseWriter, r *http.Request) {
	if _, err := h.currentUser(r); err == nil { // сессия жива?
		http.Redirect(w, r, "/admin", http.StatusSeeOther) // 303: браузер сделает GET /admin
		return
	}
	h.render(w, "login.html", nil) // иначе показываем форму
}

// pageAdmin — таблица всех заявок. Доступ сюда уже проверен middleware requireAuth.
func (h *Handler) pageAdmin(w http.ResponseWriter, r *http.Request) {
	subs, err := h.store.ListSubmissions() // берём заявки + файлы из базы
	if err != nil {
		log.Printf("список заявок: %v", err) // детали ошибки — только в журнал
		http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	h.render(w, "admin.html", subs) // передаём срез в шаблон: {{range .}}...{{end}}
}
