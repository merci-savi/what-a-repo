// Файл api.go — приём данных от студентов: форма, файл, а также удаление заявок админом.
package handler

import (
	"crypto/rand"   // случайное имя файла на диске
	"encoding/hex"  // байты имени → строка
	"encoding/json" // разбор JSON формы
	"errors"        // сравнение с ErrNotFound
	"io"            // io.Copy — потоковое копирование файла (не грузим весь в память)
	"log"           // журнал
	"net/http"      // запросы/ответы, лимиты тела
	"os"            // создание/удаление файлов
	"path/filepath" // Ext — расширение, Join — безопасная склейка путей
	"slices"        // Contains — есть ли элемент в срезе (Go 1.21+)
	"strconv"       // строки ↔ числа
	"strings"       // TrimPrefix, ToLower

	"student-activity/internal/model"   // справочники групп и мероприятий
	"student-activity/internal/storage" // база + ErrNotFound
)

const maxUploadSize = 10 << 20 // лимит файла: 10 МБ (10 * 1024 * 1024 байт)

// apiCreateSubmission — POST /api/submissions.
// Фронт шлёт коды: {"group":"group111","activities":["activity0","activity1"]}.
func (h *Handler) apiCreateSubmission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Group      string   `json:"group"`      // код группы, например "group111"
		Activities []string `json:"activities"` // коды мероприятий: ["activity0", ...]
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // JSON формы больше 1 МБ нам не нужен
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Некорректный запрос"})
		return
	}

	// ВАЛИДАЦИЯ на сервере обязательна: клиентские проверки обходятся за секунды
	// (curl, DevTools). Сервер — последний и единственный надёжный рубеж.
	groupCode := strings.TrimPrefix(req.Group, "group") // "group111" → "111"
	if !slices.Contains(model.Groups, groupCode) {      // есть ли такая группа в справочнике?
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Выберите группу из списка"})
		return
	}
	if len(req.Activities) == 0 { // пустой список мероприятий — заявка бессмысленна
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Отметьте хотя бы одно мероприятие"})
		return
	}

	seen := make(map[int]bool) // защита от дублей: "activity0" дважды = одно мероприятие
	var names []string         // сюда соберём ЧЕЛОВЕКОЧИТАЕМЫЕ названия для базы
	for _, code := range req.Activities { // перебираем присланные коды
		idx, err := strconv.Atoi(strings.TrimPrefix(code, "activity")) // "activity1" → 1
		if err != nil || idx < 0 || idx >= len(model.Activities) {     // не число или вне списка
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Неизвестное мероприятие"})
			return
		}
		if !seen[idx] { // ещё не встречалось?
			seen[idx] = true                            // отмечаем
			names = append(names, model.Activities[idx]) // индекс → название из справочника
		}
	}

	id, err := h.store.CreateSubmission(groupCode, names) // сохраняем и получаем номер заявки
	if err != nil {
		log.Printf("создание заявки: %v", err) // детали — в журнал
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить отчёт"})
		return
	}
	// 201 Created — «ресурс создан». Фронт получит id и перейдёт на /upload?id=...
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// apiUpload — POST /api/upload, multipart-форма: поле submission_id + файл file.
func (h *Handler) apiUpload(w http.ResponseWriter, r *http.Request) {
	// Жёсткий лимит на ВСЁ тело запроса (файл + поля + служебные границы multipart).
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+4096)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil { // разбираем multipart
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Файл слишком большой (максимум 10 МБ)"})
		return
	}
	defer r.MultipartForm.RemoveAll() // удаляем временные файлы парсера при выходе из функции

	// Номер заявки, к которой крепим документ (пришёл скрытым полем формы).
	id, err := strconv.ParseInt(r.FormValue("submission_id"), 10, 64) // строка → int64, десятичная
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Не указан номер отчёта"})
		return
	}
	exists, err := h.store.SubmissionExists(id) // заявка вообще существует?
	if err != nil {
		log.Printf("проверка заявки: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Внутренняя ошибка"})
		return
	}
	if !exists {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Отчёт не найден — сначала заполните форму"})
		return
	}

	file, header, err := r.FormFile("file") // file — поток, header — имя/размер от клиента
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Файл не получен"})
		return
	}
	defer file.Close() // закрываем поток при любом выходе

	// Расширение берём из имени, приводим к нижнему регистру ("PDF" → ".pdf").
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{ // белый список: разрешено ТОЛЬКО перечисленное
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".pdf": true,
	}
	if !allowed[ext] { // отсутствующий ключ карты вернёт false — удобно!
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Допустимы только JPG, PNG, WEBP или PDF"})
		return
	}
	// Имя от клиента ОПАСНО использовать на диске ("../../evil.exe", перезапись чужого файла).
	// Генерируем своё случайное имя; расширение уже проверено по белому списку.
	buf := make([]byte, 16) // 16 байт случайности — коллизии исключены
	if _, err := rand.Read(buf); err != nil {
		log.Printf("имя файла: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Внутренняя ошибка"})
		return
	}
	storedName := hex.EncodeToString(buf) + ext // например "9f3a...e1.pdf"
	dstPath := filepath.Join(h.cfg.UploadDir, storedName) // путь внутри папки загрузок

	dst, err := os.Create(dstPath) // создаём файл на диске
	if err != nil {
		log.Printf("создать файл: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить файл"})
		return
	}
	// io.Copy копирует ПОТОКОМ, кусками — файл на 10 МБ не окажется целиком в RAM.
	written, err := io.Copy(dst, file)
	if err != nil { // обрыв соединения посреди загрузки и т.п.
		dst.Close()          // закрываем...
		os.Remove(dstPath)   // ...и убираем недописанный огрызок
		log.Printf("копирование файла: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить файл"})
		return
	}
	if err := dst.Close(); err != nil { // Close тоже может вернуть ошибку записи (диск полон)
		os.Remove(dstPath)
		log.Printf("закрыть файл: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить файл"})
		return
	}

	// Файл на диске есть — фиксируем метаданные в базе (размер — реально записанный).
	if err := h.store.SaveFile(id, header.Filename, storedName, written); err != nil {
		os.Remove(dstPath) // база не приняла → файл-сирота не нужен, подтираем
		log.Printf("файл в БД: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить файл"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "name": header.Filename})
}

// apiDeleteSubmission — DELETE /api/submissions/{id}. Доступно только админу (requireAuth).
func (h *Handler) apiDeleteSubmission(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64) // PathValue — сегмент {id} из маршрута
	if err != nil {                                        // в URL не число
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Некорректный номер заявки"})
		return
	}
	names, err := h.store.DeleteSubmission(id) // удаляем строки (files уйдут по CASCADE)
	if errors.Is(err, storage.ErrNotFound) {   // такой заявки нет
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Заявка не найдена"})
		return
	}
	if err != nil { // прочий сбой базы
		log.Printf("удаление заявки: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось удалить"})
		return
	}
	// Строки удалены — теперьbest-effort чистим диск. Осиротевший файл не ломает приложение,
	// поэтому ошибку удаления только логируем, ответ клиенту не портим.
	for _, name := range names {
		if err := os.Remove(filepath.Join(h.cfg.UploadDir, name)); err != nil && !os.IsNotExist(err) {
			log.Printf("удалить файл %s: %v", name, err) // уже исчез — не ошибка
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
