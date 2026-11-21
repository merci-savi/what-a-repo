package main // исполняемый пакет

import (
	"context"   // сигнал остановки
	"errors"    // errors.Is
	"log"       // журнал
	"net/http"  // сервер
	"os"        // os.Interrupt
	"os/signal" // перехват Ctrl+C / SIGTERM
	"syscall"   // SIGTERM
	"time"      // таймауты

	"student-activity/internal/caldavbackend" // наш CalDAV-бэкенд
	"student-activity/internal/config"        // настройки из .env
	"student-activity/internal/handler"       // HTTP-обработчики
	"student-activity/internal/icalgen"       // генератор iCal
	"student-activity/internal/middleware"    // логи/паники/заголовки
	"student-activity/internal/storage"       // SQLite
)

func main() {
	cfg := config.Load() // читаем .env + переменные окружения

	store, err := storage.New(cfg.DBPath) // открываем базу, создаём таблицы
	if err != nil {
		log.Fatalf("БД: %v", err) // без базы работать нельзя
	}
	defer store.Close() // закроем при выходе

	// Админ из .env: создаётся ТОЛЬКО если в БД нет ни одного админа.
	if err := store.EnsureAdmin(cfg.AdminLogin, cfg.AdminPassword); err != nil {
		log.Fatalf("админ: %v", err)
	}

	gen := icalgen.New(cfg.Host, cfg.SemesterStart, cfg.SemesterEnd)              // генератор календарей
	backend := &caldavbackend.Backend{Store: store, Gen: gen, Prefix: "/caldav/"} // CalDAV поверх БД

	h, err := handler.New(store, cfg, gen, backend) // шаблоны, папки, зависимости
	if err != nil {
		log.Fatalf("обработчики: %v", err)
	}

	mux := http.NewServeMux() // диспетчер маршрутов
	h.RegisterRoutes(mux)     // регистрируем страницы, API, iCal, CalDAV

	var root http.Handler = mux           // оборачиваем «луковицей» middleware
	root = middleware.SecureHeaders(root) // заголовки безопасности
	root = middleware.Logger(root)        // журнал запросов
	root = middleware.Recover(root)       // паники не роняют сервер

	srv := &http.Server{ // сервер с таймаутами (защита от «медленных» клиентов)
		Addr:              cfg.Addr,
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second, // CSV до 32 МБ может грузиться долго
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM) // Ctrl+C
	defer stop()

	go func() { // сервер — в отдельной горутине, main ждёт сигнала
		log.Printf("Сайт: http://%s (вход первым экраном)", cfg.Host)
		log.Printf("CalDAV: http://%s/caldav/ (логин + пароль или iCal-хеш)", cfg.Host)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("сервер: %v", err) // ErrServerClosed — нормальная остановка
		}
	}()

	<-ctx.Done() // ждём сигнал остановки
	log.Println("Останавливаемся, дописываем текущие ответы...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // 10с на дописывание
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("принудительная остановка: %v", err)
	}
	log.Println("Готово.")
}
