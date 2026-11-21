package handler // вход, регистрация, выход, охрана маршрутов

import (
	"crypto/rand"  // токены
	"encoding/hex" // hex
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp" // проверка логина
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"student-activity/internal/model"
	"student-activity/internal/storage"
)

const sessionCookie = "session_token" // имя куки
const sessionTTL = 24 * time.Hour     // сутки

var loginRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`) // допустимые логины

func (h *Handler) currentUser(r *http.Request) (model.User, error) { // сессия из куки
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return model.User{}, storage.ErrNotFound
	}
	return h.store.SessionUser(c.Value)
}

// requireRoles — охранник: пускает только перечисленные роли.
func (h *Handler) requireRoles(next http.Handler, roles ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := h.currentUser(r)
		isAPI := r.Method != http.MethodGet || strings.HasPrefix(r.URL.Path, "/api/")
		if err != nil { // не вошёл
			if isAPI {
				errJSON(w, http.StatusUnauthorized, "Требуется вход")
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		for _, role := range roles {
			if u.Role == role {
				next.ServeHTTP(w, r) // роль подходит — пропускаем
				return
			}
		}
		if isAPI { // чужая роль
			errJSON(w, http.StatusForbidden, "Недостаточно прав")
		} else {
			http.Redirect(w, r, homeFor(u.Role), http.StatusSeeOther) // на свою страницу
		}
	})
}

func (h *Handler) startSession(w http.ResponseWriter, userID int64) error { // выдать билет
	b := make([]byte, 32) // 256 бит случайности
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := hex.EncodeToString(b)
	expires := time.Now().Add(sessionTTL)
	if err := h.store.CreateSession(token, userID, expires); err != nil {
		return err
	}
	if err := h.store.DeleteExpiredSessions(); err != nil {
		log.Printf("очистка сессий: %v", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires,
	})
	return nil
}

func (h *Handler) pageLogin(w http.ResponseWriter, r *http.Request) {
	if u, err := h.currentUser(r); err == nil { // уже вошёл — сразу домой
		http.Redirect(w, r, homeFor(u.Role), http.StatusSeeOther)
		return
	}
	h.render(w, "login.html", nil)
}

func (h *Handler) pageRegister(w http.ResponseWriter, r *http.Request) {
	if u, err := h.currentUser(r); err == nil {
		http.Redirect(w, r, homeFor(u.Role), http.StatusSeeOther)
		return
	}
	h.render(w, "register.html", nil)
}

func (h *Handler) apiLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // лимит тела
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusBadRequest, "Некорректный запрос")
		return
	}
	u, err := h.store.UserByUsername(req.Username)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) { // одинаковый ответ для обоих случаев
			errJSON(w, http.StatusUnauthorized, "Неверный логин или пароль")
			return
		}
		log.Printf("вход: %v", err)
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		errJSON(w, http.StatusUnauthorized, "Неверный логин или пароль")
		return
	}
	if err := h.startSession(w, u.ID); err != nil {
		log.Printf("сессия: %v", err)
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect": homeFor(u.Role)}) // роль → страница
}

// apiRegister — САМОрегистрация студента (преподавателей создаёт только админ).
func (h *Handler) apiRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusBadRequest, "Некорректный запрос")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !loginRe.MatchString(req.Username) {
		errJSON(w, http.StatusBadRequest, "Логин: 3–32 символа, латиница/цифры/._-")
		return
	}
	if len(req.Password) < 6 {
		errJSON(w, http.StatusBadRequest, "Пароль минимум 6 символов")
		return
	}
	if _, err := h.store.UserByUsername(req.Username); err == nil { // логин занят
		errJSON(w, http.StatusConflict, "Логин уже занят")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	id, err := h.store.CreateUser(req.Username, string(hash), model.RoleStudent, "")
	if err != nil {
		log.Printf("регистрация: %v", err)
		errJSON(w, http.StatusInternalServerError, "Не удалось создать аккаунт")
		return
	}
	if err := h.startSession(w, id); err != nil { // сразу входим
		log.Printf("сессия: %v", err)
		errJSON(w, http.StatusInternalServerError, "Внутренняя ошибка")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "redirect": "/student"})
}

func (h *Handler) apiLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := h.store.DeleteSession(c.Value); err != nil {
			log.Printf("выход: %v", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect": "/login"})
}
