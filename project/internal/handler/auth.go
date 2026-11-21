// Файл auth.go — всё про вход: сессии, куки, защита маршрутов.
package handler

import (
	"crypto/rand"  // КРИПТОстойкий генератор случайных чисел (не math/rand — тот предсказуем!)
	"encoding/hex" // байты → шестнадцатеричная строка (токен в куки)
	"encoding/json"// разбор JSON-тела запроса входа
	"errors"       // errors.Is — сравнение с сигнальной ошибкой
	"log"          // журнал внутренних ошибок
	"net/http"     // куки, редиректы, статусы
	"strings"      // HasPrefix — отличаем API от страниц
	"time"         // срок жизни сессии

	"golang.org/x/crypto/bcrypt" // сравнение пароля с хешем

	"student-activity/internal/model"   // тип User
	"student-activity/internal/storage" // ErrNotFound и методы сессий
)

const sessionCookieName = "session_token" // имя нашей куки
const sessionTTL = 24 * time.Hour         // сессия живёт сутки, потом нужен повторный вход

// currentUser достаёт куку и спрашивает базу: «чей это билет и не протух ли он?»
func (h *Handler) currentUser(r *http.Request) (model.User, error) {
	cookie, err := r.Cookie(sessionCookieName) // ищем куку по имени
	if err != nil {                            // куки нет вообще
		return model.User{}, storage.ErrNotFound // трактуем как «не вошёл»
	}
	return h.store.SessionUser(cookie.Value) // база проверит токен и срок годности
}

// requireAuth — «охранник» вокруг защищённых маршрутов.
// Живая сессия → пропускаем; иначе API получает 401 JSON, а страница — редирект на /login.
func (h *Handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // замыкание
		if _, err := h.currentUser(r); err != nil { // билета нет / протух / ошибка БД
			if strings.HasPrefix(r.URL.Path, "/api/") { // для API редирект бессмысленен
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Требуется вход"})
			} else { // человека с браузера отправляем на форму входа
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return // ВАЖНО: без return запрос прошёл бы дальше — дыра в безопасности
		}
		next.ServeHTTP(w, r) // билет в порядке — передаём настоящему обработчику
	})
}

// apiLogin обрабатывает POST /api/login с JSON {"username": "...", "password": "..."}.
func (h *Handler) apiLogin(w http.ResponseWriter, r *http.Request) {
	// Анонимная структура прямо на месте — описываем форму запроса без лишнего типа.
	var req struct {
		Username string `json:"username"` // теги json связывают поля с ключами JSON
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // тело максимум 1 МБ — защита от гигантских запросов
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { // разбираем JSON в структуру
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Некорректный запрос"})
		return
	}
	// Примечание: приём ТОЛЬКО JSON заодно мешает простой CSRF-атаке HTML-формой
	// (форма шлёт urlencoded — Decode его отвергнет), а кука ниже имеет SameSite=Lax.

	user, err := h.store.UserByUsername(req.Username) // ищем пользователя по логину
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) { // логин не найден
			// Ответ ОДИНАКОВЫЙ для «нет логина» и «не тот пароль» —
			// не подсказываем злоумышленнику, какие логины существуют.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Неверный логин или пароль"})
			return
		}
		log.Printf("вход: %v", err) // сбой базы — 500, детали в журнале
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Внутренняя ошибка"})
		return
	}
	// bcrypt сам извлекает соль из хеша и сравнивает; err != nil = пароль не совпал.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Неверный логин или пароль"})
		return
	}

	// Пароль верный — выдаём «билет»: 32 случайных байта = 256 бит энтропии,
	// угадать такой токен практически невозможно.
	tokenBytes := make([]byte, 32) // выделяем срез нужной длины
	if _, err := rand.Read(tokenBytes); err != nil { // заполняем криптографической случайностью
		log.Printf("генерация токена: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Внутренняя ошибка"})
		return
	}
	token := hex.EncodeToString(tokenBytes) // байты → строка из 64 hex-символов
	expires := time.Now().Add(sessionTTL)   // момент протухания: сейчас + 24 часа

	if err := h.store.CreateSession(token, user.ID, expires); err != nil { // билет → в базу
		log.Printf("сессия: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Внутренняя ошибка"})
		return
	}
	// Заодно выметаем старые билеты; ошибка очистки не критична — только логируем.
	if err := h.store.DeleteExpiredSessions(); err != nil {
		log.Printf("очистка сессий: %v", err)
	}

	http.SetCookie(w, &http.Cookie{ // отправляем браузеру заголовок Set-Cookie
		Name:     sessionCookieName,          // имя куки
		Value:    token,                      // сам билет
		Path:     "/",                        // кука шлётся на все пути сайта
		HttpOnly: true,                       // JavaScript куку НЕ видит → кража через XSS невозможна
		SameSite: http.SameSiteLaxMode,       // не шлём куку при чужих跨-site POST → защита от CSRF
		Expires:  expires,                    // браузер забудет куку вместе с сессией
		// Secure: true, // включите при работе по HTTPS: кука только по шифрованному каналу
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect": "/admin"}) // фронт сделает переход
}

// apiLogout — POST /api/logout: гасим билет и в базе, и в браузере.
// Не защищаем requireAuth: выйти можно всегда, даже с протухшей сессией.
func (h *Handler) apiLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil { // кука есть?
		if err := h.store.DeleteSession(cookie.Value); err != nil { // удаляем билет из базы
			log.Printf("удаление сессии: %v", err) // не фатально: куку всё равно сотрём
		}
	}
	http.SetCookie(w, &http.Cookie{ // MaxAge < 0 = команда браузеру УДАЛИТЬ куку
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect": "/login"})
}
