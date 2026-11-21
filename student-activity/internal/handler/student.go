package handler // страница студента: группа + активности + ссылки календаря

import (
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"student-activity/internal/model"
)

func (h *Handler) pageStudent(w http.ResponseWriter, r *http.Request) {
	u, _ := h.currentUser(r) // роль уже проверена requireRoles
	groups, extras := h.catalogs()
	acts, err := h.store.StudentActivities(u.ID) // сохранённые активности
	if err != nil {
		log.Printf("активности: %v", err)
	}
	var actCodes []int // индексы сохранённых активностей (для чекбоксов)
	for _, a := range acts {
		if i := slices.Index(extras, a); i >= 0 {
			actCodes = append(actCodes, i)
		}
	}

	data := map[string]any{
		"Username": u.Username,
		"AppData": appDataJSON(map[string]any{ // было appData(...)
			"groups": groups, "activities": extras,
			// ...остальные поля без изменений...

		}),
	}
	h.render(w, "student.html", data)
}

// apiStudentProfile — сохранение выбора и выдача ссылок календаря.
func (h *Handler) apiStudentProfile(w http.ResponseWriter, r *http.Request) {
	u, _ := h.currentUser(r)
	var req struct {
		Group      string   `json:"group"`      // "group111"
		Activities []string `json:"activities"` // ["activity0", ...]
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusBadRequest, "Некорректный запрос")
		return
	}
	groups, extras := h.catalogs() // серверная валидация по тем же спискам
	group := strings.TrimPrefix(req.Group, "group")
	if !slices.Contains(groups, group) {
		errJSON(w, http.StatusBadRequest, "Выберите группу из списка")
		return
	}
	seen := make(map[int]bool)
	var names []string
	for _, code := range req.Activities {
		i, err := strconv.Atoi(strings.TrimPrefix(code, "activity")) // "activity1" → 1
		if err != nil || i < 0 || i >= len(extras) {
			errJSON(w, http.StatusBadRequest, "Неизвестная активность")
			return
		}
		if !seen[i] { // без дублей
			seen[i] = true
			names = append(names, extras[i])
		}
	}
	if err := h.store.SetStudentProfile(u.ID, group, names); err != nil {
		log.Printf("профиль: %v", err)
		errJSON(w, http.StatusInternalServerError, "Не удалось сохранить")
		return
	}
	u.GroupNumber = group // обновляем локальную копию для ссылок
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "links": h.links(u)})
}

var _ = model.RoleStudent // модель используется в других файлах пакета
