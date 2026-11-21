module student-activity // имя модуля для импортов

go 1.22 // роутер с методами и {параметрами} — стандарт с 1.22

require (
	github.com/emersion/go-ical v0.0.0-20261112170022-46c5b53a1723
	github.com/emersion/go-webdav v0.7.0
	github.com/joho/godotenv v1.5.1 // чтение .env
	golang.org/x/crypto v0.31.0 // bcrypt
	modernc.org/sqlite v1.34.4 // SQLite на чистом Go, без C-компилятора
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/teambition/rrule-go v1.8.2 // indirect
	golang.org/x/sys v0.28.0 // indirect
	modernc.org/gc/v3 v3.0.0-20240107210532-573471604cb6 // indirect
	modernc.org/libc v1.55.3 // indirect
	modernc.org/mathutil v1.6.0 // indirect
	modernc.org/memory v1.8.0 // indirect
	modernc.org/strutil v1.2.0 // indirect
	modernc.org/token v1.1.0 // indirect
)

// github.com/emersion/go-ical и github.com/emersion/go-webdav
// добавятся автоматически при `go mod tidy` (он подтянет последние версии).
