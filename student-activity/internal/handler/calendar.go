package handler // публичная iCal-лента и Basic-auth для CalDAV

import (
	"context"
	"crypto/subtle" // сравнение без утечки времени
	"errors"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"student-activity/internal/caldavbackend"
	"student-activity/internal/icalgen"
	"student-activity/internal/storage"
)

// icalFeed — GET /ical/{hash}.ics. Хеш — секрет: кто знает ссылку, тот подписан.
func (h *Handler) icalFeed(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimSuffix(r.PathValue("hash"), ".ics") // на случай маршрута без суффикса
	u, err := h.store.UserByIcalHash(hash)                  // пользователь по хешу
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r) // не раскрываем существование хешей
			return
		}
		log.Printf("лента: %v", err)
		http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	lessons, err := h.store.LessonsForUser(u) // группа + активности, свежие из БД
	if err != nil {
		log.Printf("лента: %v", err)
		http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	raw, err := icalgen.Encode(h.gen.Calendar(u, lessons, true)) // METHOD:PUBLISH
	if err != nil {
		log.Printf("ical: %v", err)
		http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="schedule.ics"`)
	w.Write(raw) // каждый запрос — актуальное расписание (SEQUENCE уже внутри)
}

// caldavBasicAuth — привязка пользователя к календарю: логин + пароль ИЛИ логин + ical-хеш.
func (h *Handler) caldavBasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, secret, ok := r.BasicAuth() // заголовок Authorization: Basic ...
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="schedule"`) // просим credentials
			http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
			return
		}
		u, err := h.store.UserByUsername(username)
		if err != nil {
			http.Error(w, "Неверные данные", http.StatusUnauthorized)
			return
		}
		passOK := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(secret)) == nil
		hashOK := subtle.ConstantTimeCompare([]byte(u.IcalHash), []byte(secret)) == 1 // хеш как пароль
		if !passOK && !hashOK {
			http.Error(w, "Неверные данные", http.StatusUnauthorized)
			return
		}
		// Кладём пользователя в контекст — CalDAV-бэкенд достанет его оттуда.
		ctx := context.WithValue(r.Context(), caldavbackend.UserKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
