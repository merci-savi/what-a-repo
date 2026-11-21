package handler // админ: заявки, CSV, преподаватели

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"student-activity/internal/model"
	"student-activity/internal/storage"
)

func (h *Handler) pageAdmin(w http.ResponseWriter, r *http.Request) {
	reqs, err := h.store.ListRequests(0) // 0 = все заявки
	if err != nil {
		log.Printf("заявки: %v", err)
		http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	// Было: map[string]any{"Requests": reqs, "AppData": appData(...)}
	// Стало: только заявки — таблица рендерится сервером, JS-данные не нужны.
	h.render(w, "admin.html", map[string]any{"Requests": reqs})
}

// apiDecide — POST /api/admin/requests/{id}/decide {"approve": true|false}.
func (h *Handler) apiDecide(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64) // {id} из маршрута
	if err != nil {
		errJSON(w, http.StatusBadRequest, "Некорректный номер")
		return
	}
	var req struct {
		Approve bool `json:"approve"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusBadRequest, "Некорректный запрос")
		return
	}
	err = h.store.DecideRequest(id, req.Approve, "")
	switch {
	case errors.Is(err, storage.ErrNotFound):
		errJSON(w, http.StatusNotFound, "Заявка не найдена")
	case errors.Is(err, storage.ErrDecided):
		errJSON(w, http.StatusConflict, "Заявка уже обработана")
	case errors.Is(err, storage.ErrRoomTaken):
		errJSON(w, http.StatusConflict, "Аудитория уже занята — отклоните или измените заявку")
	case err != nil:
		log.Printf("решение: %v", err)
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true}) // расписание обновлено в той же транзакции
	}
}

func (h *Handler) pageUpload(w http.ResponseWriter, r *http.Request) {
	h.render(w, "upload.html", nil)
}

const maxCSVSize = 32 << 20 // 32 МБ на файл расписания

// apiUploadCSV — приём .csv, разбор, UPSERT в БД, архивирование файла.
func (h *Handler) apiUploadCSV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCSVSize+4096) // жёсткий лимит
	if err := r.ParseMultipartForm(maxCSVSize); err != nil {
		errJSON(w, http.StatusBadRequest, "Файл слишком большой (макс. 32 МБ)")
		return
	}
	defer r.MultipartForm.RemoveAll() // чистим временные файлы
	file, header, err := r.FormFile("file")
	if err != nil {
		errJSON(w, http.StatusBadRequest, "Файл не получен")
		return
	}
	defer file.Close()
	if !strings.EqualFold(filepath.Ext(header.Filename), ".csv") {
		errJSON(w, http.StatusBadRequest, "Нужен файл .csv")
		return
	}
	raw, err := io.ReadAll(file) // читаем целиком (≤32 МБ) — parse + архив
	if err != nil {
		errJSON(w, http.StatusBadRequest, "Не удалось прочитать файл")
		return
	}
	lessons, warnings, skipped, err := parseCSV(raw) // разбор и валидация
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	added, updated, unchanged, err := h.store.ImportLessons(lessons) // UPSERT + sequence
	if err != nil {
		log.Printf("импорт: %v", err)
		errJSON(w, http.StatusInternalServerError, "Ошибка записи в БД")
		return
	}
	// Архив исходника (аудит): имя с временем, ничего не перезаписываем.
	archive := filepath.Join(h.cfg.UploadDir, time.Now().Format("20060102-150405")+".csv")
	if err := os.WriteFile(archive, raw, 0o644); err != nil {
		log.Printf("архив CSV: %v", err) // не критично — только журнал
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added": added, "updated": updated, "unchanged": unchanged,
		"skipped": skipped, "warnings": warnings,
	})
}

// parseCSV разбирает CSV с колонками:
// id, prepod_name, group_number, lesson, place, lesson_number, numerator, denominator, type
// (+ опциональный day: 1=Пн..6=Сб; без него строки попадают на понедельник с предупреждением).
func parseCSV(raw []byte) (lessons []model.Lesson, warnings []string, skipped int, err error) {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // убираем BOM (Excel любит его)
	br := bufio.NewReader(bytes.NewReader(raw))
	peek, _ := br.Peek(1024) // смотрим начало, чтобы угадать разделитель
	delim := ','
	if bytes.Contains(peek, []byte(";")) && !bytes.Contains(peek, []byte(",")) {
		delim = ';' // Excel в русской локали часто сохраняет через ';'
	}
	cr := csv.NewReader(br)
	cr.Comma = delim
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1 // разрешаем разное число колонок (проверим сами)
	records, err := cr.ReadAll()
	if err != nil {
		return nil, nil, 0, fmt.Errorf("CSV не разобран: %v", err)
	}
	if len(records) < 2 {
		return nil, nil, 0, errors.New("CSV пуст: нужна строка заголовков и данные")
	}
	col := make(map[string]int) // имя колонки → индекс
	for i, name := range records[0] {
		col[strings.ToLower(strings.TrimSpace(name))] = i
	}
	get := func(row []string, name string) string { // безопасное чтение ячейки
		if i, ok := col[name]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	for _, name := range []string{"id", "prepod_name", "group_number", "lesson",
		"place", "lesson_number", "numerator", "denominator", "type"} {
		if _, ok := col[name]; !ok {
			return nil, nil, 0, fmt.Errorf("нет обязательной колонки %q", name)
		}
	}
	_, hasDay := col["day"] // день недели опционален
	if !hasDay {
		warnings = append(warnings, "Нет колонки day — все занятия поставлены на понедельник (добавьте day: 1–6)")
	}
	warn := func(msg string) { // копим предупреждения, но не больше 30
		if len(warnings) < 30 {
			warnings = append(warnings, msg)
		}
	}
	for n, row := range records[1:] {
		line := n + 2 // номер строки для сообщений (1 — заголовок)
		l := model.Lesson{
			CsvID:       get(row, "id"),
			PrepodName:  get(row, "prepod_name"),
			GroupNumber: get(row, "group_number"),
			Title:       get(row, "lesson"),
			Place:       get(row, "place"),
			Type:        get(row, "type"),
			Source:      "csv",
		}
		if l.CsvID == "" || l.GroupNumber == "" || l.Title == "" || l.Place == "" {
			skipped++
			warn(fmt.Sprintf("строка %d: пустые id/группа/предмет/аудитория — пропуск", line))
			continue
		}
		pair, err := strconv.Atoi(get(row, "lesson_number"))
		if err != nil || pair < 1 || pair > len(model.PairNumbers) {
			skipped++
			warn(fmt.Sprintf("строка %d: неверный lesson_number — пропуск", line))
			continue
		}
		l.Pair = pair
		l.Day = 1 // по умолчанию понедельник
		if hasDay {
			d, err := strconv.Atoi(get(row, "day"))
			if err != nil || d < 1 || d > 6 {
				skipped++
				warn(fmt.Sprintf("строка %d: day должен быть 1–6 — пропуск", line))
				continue
			}
			l.Day = d
		}
		num := truthy(get(row, "numerator"))   // числитель?
		den := truthy(get(row, "denominator")) // знаменатель?
		switch {
		case num && den:
			l.WeekType = model.WeekAll
		case num:
			l.WeekType = model.WeekNum
		case den:
			l.WeekType = model.WeekDen
		default: // 0/0 — занятие не проводится никогда
			skipped++
			warn(fmt.Sprintf("строка %d: numerator=0 и denominator=0 — пропуск", line))
			continue
		}
		lessons = append(lessons, l)
	}
	return lessons, warnings, skipped, nil
}

func truthy(s string) bool { // "1", "true", "да", "числ" → true; "0"/пусто → false
	s = strings.ToLower(strings.TrimSpace(s))
	return s != "" && s != "0" && s != "false" && s != "нет" && s != "no"
}

func (h *Handler) pageTeachers(w http.ResponseWriter, r *http.Request) {
	teachers, err := h.store.ListTeachers()
	if err != nil {
		log.Printf("преподаватели: %v", err)
		http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	h.render(w, "teachers.html", map[string]any{"Teachers": teachers})
}

// apiCreateTeacher — создание учётки преподавателя (только админ).
func (h *Handler) apiCreateTeacher(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		FullName string `json:"full_name"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusBadRequest, "Некорректный запрос")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.FullName = strings.TrimSpace(req.FullName)
	if !loginRe.MatchString(req.Username) {
		errJSON(w, http.StatusBadRequest, "Логин: 3–32 символа, латиница/цифры/._-")
		return
	}
	if len(req.Password) < 6 {
		errJSON(w, http.StatusBadRequest, "Пароль минимум 6 символов")
		return
	}
	if req.FullName == "" {
		errJSON(w, http.StatusBadRequest, "Укажите ФИО преподавателя")
		return
	}
	if _, err := h.store.UserByUsername(req.Username); err == nil {
		errJSON(w, http.StatusConflict, "Логин уже занят")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	if _, err := h.store.CreateUser(req.Username, string(hash), model.RoleTeacher, req.FullName); err != nil {
		log.Printf("создание преподавателя: %v", err)
		errJSON(w, http.StatusInternalServerError, "Не удалось создать")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}
