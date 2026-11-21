// Пакет main — особый: только он собирается в исполняемый файл,
// и только в нём функция main() — точка входа программы.
package main

// import перечисляет пакеты, кодом которых мы пользуемся.
import (
	"context"  // контекст: способ передать сигнал «пора завершаться»
	"errors"   // работа с ошибками: сравнение errors.Is
	"log"      // простой логгер: печатает сообщения с датой и временем
	"net/http" // встроенный HTTP-сервер Go — нам не нужен сторонний фреймворк
	"os"       // доступ к ОС: сигналы (Ctrl+C)
	"os/signal"// удобный перехват сигналов операционной системы
	"syscall"  // константы сигналов, например SIGTERM (его шлёт systemd/docker)
	"time"     // длительности: time.Second и т.п.

	"student-activity/internal/config"     // наш пакет настроек
	"student-activity/internal/handler"    // наш пакет обработчиков запросов
	"student-activity/internal/middleware" // наш пакет промежуточных слоёв
	"student-activity/internal/storage"    // наш пакет работы с БД
)

func main() { // с этой функции начинается выполнение программы
	// ---------- 1. Настройки ----------
	cfg := config.Load() // читаем адрес, пути, логин/пароль админа (см. config.go)

	// ---------- 2. База данных ----------
	store, err := storage.New(cfg.DBPath) // открываем SQLite и создаём таблицы
	if err != nil {                       // в Go ошибка — обычное значение; проверяем КАЖДУЮ
		log.Fatalf("не открыть БД: %v", err) // Fatalf = напечатать и завершить программу (код 1)
	}
	defer store.Close() // defer выполнится при выходе из main — закроем БД при любой остановке

	// Создаём администратора при первом запуске (если таблица users пуста).
	if err := store.EnsureAdmin(cfg.AdminUser, cfg.AdminPassword); err != nil {
		log.Fatalf("не создать администратора: %v", err) // без админа приложение бесполезно — выходим
	}

	// ---------- 3. Обработчики и шаблоны ----------
	h, err := handler.New(store, cfg) // парсим HTML-шаблоны, создаём папку загрузок
	if err != nil {
		log.Fatalf("не инициализировать обработчики: %v", err) // например, нет папки web/templates
	}

	// ---------- 4. Маршрутизатор ----------
	mux := http.NewServeMux() // ServeMux — «диспетчер»: подбирает обработчик по методу и пути
	h.RegisterRoutes(mux)     // регистрируем все наши маршруты (см. handler.go)

	// Оборачиваем роутер в middleware — «слои луковицы».
	// Запрос пройдёт: Recover → Logger → SecureHeaders → mux, ответ пойдёт обратно.
	var root http.Handler = mux                  // http.Handler — интерфейс с методом ServeHTTP
	root = middleware.SecureHeaders(root)        // внутренний слой: заголовки безопасности
	root = middleware.Logger(root)               // средний слой: журнал каждого запроса
	root = middleware.Recover(root)              // внешний слой: ловит паники, сервер не падает

	// ---------- 5. HTTP-сервер ----------
	srv := &http.Server{ // & — берём указатель на структуру (не копируем её)
		Addr:              cfg.Addr,             // адрес, например ":8080" (все интерфейсы, порт 8080)
		Handler:           root,                 // наш роутер в обёртках
		ReadHeaderTimeout: 5 * time.Second,      // лимит на чтение заголовков (защита от «медленных» атак)
		ReadTimeout:       30 * time.Second,     // лимит на чтение всего запроса (включая файл)
		WriteTimeout:      60 * time.Second,     // лимит на отдачу ответа
		IdleTimeout:       120 * time.Second,    // сколько держим простаивающее keep-alive соединение
	}

	// ---------- 6. Graceful shutdown («мягкая остановка») ----------
	// NotifyContext вернёт контекст, который «отменится» при Ctrl+C или SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop() // освобождаем ресурсы перехватчика сигналов при выходе

	// Запускаем сервер в ОТДЕЛЬНОЙ горутине (go func — лёгкий параллельный поток),
	// потому что ListenAndServe блокирует: он работает, пока сервер жив.
	go func() {
		log.Printf("Сервер запущен: http://localhost%s", cfg.Addr) // подсказка в консоли
		log.Printf("Админ-панель: http://localhost%s/login (логин: %s)", cfg.Addr, cfg.AdminUser)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// ErrServerClosed — НОРМАЛЬНАЯ ошибка после Shutdown(), её игнорируем.
			log.Fatalf("сервер упал: %v", err) // любая другая (порт занят и т.п.) — фатальна
		}
	}()

	<-ctx.Done() // блокируемся здесь, пока контекст не отменён (читаем из канала)
	log.Println("Сигнал остановки получен, дописываем текущие ответы...")

	// Даём серверу максимум 10 секунд, чтобы завершить текущие запросы (например, загрузку файла).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel() // отменяем таймер, чтобы не держать ресурсы
	if err := srv.Shutdown(shutdownCtx); err != nil { // Shutdown = не принимаем новые, дожидаем старые
		log.Printf("принудительная остановка: %v", err) // не успели за 10с — просто логируем
	}
	log.Println("Сервер остановлен. База закрыта (defer).") // чистый выход
}
