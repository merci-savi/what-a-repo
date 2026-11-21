package config // настройки приложения

import (
	"log"  // предупреждения о плохих датах
	"os"   // переменные окружения
	"time" // даты семестра

	"github.com/joho/godotenv" // загрузка .env в переменные окружения
)

type Config struct {
	Addr          string    // адрес сервера
	Host          string    // хост для ссылок (без схемы)
	DBPath        string    // путь к SQLite
	UploadDir     string    // архив CSV
	AdminLogin    string    // логин админа из .env
	AdminPassword string    // пароль админа из .env
	SemesterStart time.Time // якорь недели числителя
	SemesterEnd   time.Time // конец расписания
}

func Load() Config {
	// godotenv.Load читает .env; если файла нет — не беда, работаем на переменных окружения.
	if err := godotenv.Load(".env"); err != nil {
		log.Println(".env не найден, использую переменные окружения и значения по умолчанию")
	}
	return Config{
		Addr:          getEnv("APP_ADDR", ":8080"),
		Host:          getEnv("APP_HOST", "localhost:8080"),
		DBPath:        getEnv("DB_PATH", "data/app.db"),
		UploadDir:     getEnv("UPLOAD_DIR", "data/uploads"),
		AdminLogin:    getEnv("ADMIN_LOGIN", "admin"),
		AdminPassword: getEnv("ADMIN_PASSWORD", "admin123"), // смените на реальном сервере!
		SemesterStart: parseDate(getEnv("SEMESTER_START", "2026-09-01"), "2026-09-01"),
		SemesterEnd:   parseDate(getEnv("SEMESTER_END", "2026-12-31"), "2026-12-31"),
	}
}

func getEnv(key, fallback string) string { // значение или запасной вариант
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDate(s, fallback string) time.Time { // дата строго в формате ГГГГ-ММ-ДД
	t, err := time.Parse("2006-01-02", s) // 2006-01-02 — эталон формата в Go
	if err != nil {
		log.Printf("дата %q не разобрана, беру %s", s, fallback)
		t, _ = time.Parse("2006-01-02", fallback) // запасная дата заведомо валидна
	}
	return t
}
