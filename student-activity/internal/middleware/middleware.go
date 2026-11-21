// Пакет middleware — «прослойки» вокруг обработчиков.
// Middleware — функция: получила http.Handler, вернула новый http.Handler с добавленным поведением.
package middleware

import (
	"log"      // журнал запросов и паник
	"net/http" // типы Handler, ResponseWriter, Request
	"time"     // замер длительности запроса
)

// loggingResponseWriter — обёртка над ResponseWriter, запоминающая код статуса.
// Встраивание (поле без имени) = все методы оригинала доступны автоматически,
// мы «переопределяем» только WriteHeader.
type loggingResponseWriter struct {
	http.ResponseWriter        // оригинальный writer от сервера
	status              int    // запомненный HTTP-статус (200, 404, ...)
}

// WriteHeader вызывается обработчиком перед отправкой ответа — перехватываем статус.
func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status                   // запоминаем себе
	w.ResponseWriter.WriteHeader(status) // и честно передаём оригиналу
}

// Logger печатает строку журнала о каждом запросе: метод, путь, статус, время, адрес клиента.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // замыкание вокруг next
		start := time.Now() // засекаем время ДО обработки
		lw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK} // 200 — дефолт,
		// если обработчик вызовет Write без WriteHeader, статус так и останется 200
		next.ServeHTTP(lw, r) // передаём запрос дальше по цепочке (внутрь «луковицы»)
		log.Printf("%s %s → %d (%s) [%s]", // после обработки печатаем итог
			r.Method, r.URL.Path, lw.status,
			time.Since(start).Round(time.Millisecond), // длительность, округлённая до мс
			r.RemoteAddr)                              // IP:порт клиента
	})
}

// Recover ловит панику (аварию) в любом обработчике ниже: сервер не падает,
// клиент получает честную 500-ю, а мы — запись в журнале.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { // defer + recover — единственный способ поймать панику
			if rec := recover(); rec != nil { // recover вернёт значение паники или nil
				log.Printf("ПАНИКА %s %s: %v", r.Method, r.URL.Path, rec)
				// Если ответ уже начал отправляться, этот вызов ничего не изменит —
				// но хотя бы не дадим упасть всему серверу.
				http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r) // обычный проход дальше; паника «раскрутится» сюда
	})
}

// SecureHeaders добавляет простые защитные заголовки ко всем ответам.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff") // браузер не «угадывает» тип файла
		w.Header().Set("X-Frame-Options", "DENY")           // сайт нельзя встроить в чужой iframe
		w.Header().Set("Referrer-Policy", "same-origin")    // не сливаем URL чужим сайтам
		next.ServeHTTP(w, r)                                // идём дальше
	})
}
