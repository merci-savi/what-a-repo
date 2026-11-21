package handler // заявки преподавателя + свободные аудитории

import (
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"student-activity/internal/model"
)

func (h *Handler) pageTeacher(w http.ResponseWriter, r *http.Request) {
	u, _ := h.currentUser(r)
	groups, extras := h.catalogs()
	reqs, err := h.store.ListRequests(u.ID) // мои заявки
	if err != nil {
		log.Printf("заявки: %v", err)
	}
	var pairs []map[string]any // подписи пар для селекта
	for _, n := range model.PairNumbers {
		pairs = append(pairs, map[string]any{"n": n, "label": model.PairLabel(n)})
	}
	var days []map[string]any
	for _, n := range model.DayNumbers {
		days = append(days, map[string]any{"n": n, "label": model.DayNames[n]})
	}
	h.render(w, "teacher.html", map[string]any{
		"Username": u.Username,
		"Requests": reqs,
		"AppData": appDataJSON(map[string]any{ // было appData(...)
			"groups": groups, "activities": extras,
		}),
	})
}

// apiFreeRooms — GET /api/rooms/free?day=1&pair=3&week=num → свободные аудитории.
func (h *Handler) apiFreeRooms(w http.ResponseWriter, r *http.Request) {
	day, err1 := strconv.Atoi(r.URL.Query().Get("day"))   // день из query-строки
	pair, err2 := strconv.Atoi(r.URL.Query().Get("pair")) // пара
	week := r.URL.Query().Get("week")                     // неделя
	if err1 != nil || err2 != nil || day < 1 || day > 6 ||
		!slices.Contains(model.PairNumbers, pair) ||
		!slices.Contains([]string{model.WeekAll, model.WeekNum, model.WeekDen}, week) {
		errJSON(w, http.StatusBadRequest, "Укажите день, пару и неделю")
		return
	}
	rooms, err := h.store.FreeRooms(day, pair, week) // фильтрация занятых в БД
	if err != nil {
		log.Printf("свободные аудитории: %v", err)
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

// apiCreateRequest — новая заявка на лекцию.
func (h *Handler) apiCreateRequest(w http.ResponseWriter, r *http.Request) {
	u, _ := h.currentUser(r)
	var req struct {
		Title  string `json:"title"`
		Target string `json:"target"` // группа или доп. активность
		Day    int    `json:"day"`
		Pair   int    `json:"pair"`
		Week   string `json:"week"`
		Place  string `json:"place"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusBadRequest, "Некорректный запрос")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Place = strings.TrimSpace(req.Place)
	groups, extras := h.catalogs()
	if req.Title == "" || len(req.Title) > 200 {
		errJSON(w, http.StatusBadRequest, "Название лекции: 1–200 символов")
		return
	}
	if !slices.Contains(groups, req.Target) && !slices.Contains(extras, req.Target) {
		errJSON(w, http.StatusBadRequest, "Выберите группу или активность")
		return
	}
	if req.Day < 1 || req.Day > 6 || !slices.Contains(model.PairNumbers, req.Pair) {
		errJSON(w, http.StatusBadRequest, "Неверные день или пара")
		return
	}
	if !slices.Contains([]string{model.WeekAll, model.WeekNum, model.WeekDen}, req.Week) {
		errJSON(w, http.StatusBadRequest, "Неверный тип недели")
		return
	}
	rooms, err := h.store.Rooms() // аудитория должна существовать...
	if err != nil {
		log.Printf("аудитории: %v", err)
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	if !slices.Contains(rooms, req.Place) {
		errJSON(w, http.StatusBadRequest, "Выберите аудиторию из списка")
		return
	}
	free, err := h.store.RoomFree(req.Day, req.Pair, req.Week, req.Place) // ...и быть свободной
	if err != nil {
		log.Printf("проверка аудитории: %v", err)
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	if !free {
		errJSON(w, http.StatusConflict, "Аудитория уже занята в это время")
		return
	}
	id, err := h.store.CreateRequest(model.LectureRequest{
		TeacherID: u.ID, Title: req.Title, Target: req.Target,
		Day: req.Day, Pair: req.Pair, WeekType: req.Week, Place: req.Place,
	})
	if err != nil {
		log.Printf("заявка: %v", err)
		errJSON(w, http.StatusInternalServerError, "Не удалось создать заявку")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": id})
}
