// Пакет storage — ЕДИНСТВЕННОЕ место, где живёт SQL.
// Остальной код вызывает методы вроде CreateSubmission и не знает, какая под капотом база.
// Захотите PostgreSQL — перепишете только этот пакет.
package storage

import (
	"database/sql" // стандартный интерфейс к SQL-базам (сам драйвер подключается отдельно)
	"encoding/json"// превращаем []string в JSON-строку и обратно (колонка activities)
	"errors"       // errors.Is / errors.New — сравнение и создание ошибок
	"fmt"          // fmt.Errorf с %w — «оборачивание» ошибок с контекстом
	"os"           // MkdirAll — создать папку для файла базы
	"path/filepath"// Dir — extracts папку из пути; работает и на Windows
	"time"         // Unix-время: храним даты как целые секунды

	"golang.org/x/crypto/bcrypt" // хеширование пароля админа при первом запуске

	"student-activity/internal/model" // наши структуры Submission/File/User

	_ "modernc.org/sqlite" // «пустой» импорт: код не вызываем, но пакет выполняет init()
	                       // и РЕГИСТРИрует драйвер с именем "sqlite" для database/sql
)

// ErrNotFound — «сигнальная» ошибка: запись не найдена.
// Обработчики сравнивают с ней через errors.Is, чтобы вернуть 404/401 вместо 500.
var ErrNotFound = errors.New("запись не найдена")

// Storage — обёртка над пулом соединений с базой.
// Поле db маленькое (неэкспортируемое): снаружи никто не выполнит SQL мимо наших методов.
type Storage struct {
	db *sql.DB // указатель на пул соединений; sql.DB потокобезопасен, один на всё приложение
}

// New открывает базу, настраивает её и создаёт таблицы. Вызывается один раз в main.
func New(dbPath string) (*Storage, error) { // возвращаем указатель и ошибку — идиома Go
	// Папки data/ может не быть — создаём её (и всех родителей) заранее.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil { // 0o755 — права rwxr-xr-x
		return nil, fmt.Errorf("создать папку БД: %w", err) // %w оборачивает исходную ошибку
	}

	// DSN — строка подключения. Параметры _pragma понимает драйвер modernc.org/sqlite:
	dsn := dbPath +
		"?_pragma=busy_timeout(5000)" + // если база занята — ждём до 5с вместо мгновенной ошибки
		"&_pragma=journal_mode(WAL)" +  // WAL: режим журнала, читатели не блокируют писателя
		"&_pragma=foreign_keys(1)"      // включаем проверку внешних ключей (по умолчанию выключена!)

	db, err := sql.Open("sqlite", dsn) // ВАЖНО: Open лишь создаёт объект, соединение ещё НЕ открыто
	if err != nil {
		return nil, fmt.Errorf("открыть БД: %w", err)
	}

	// SQLite допускает только ОДНОГО писателя. Пул из одного соединения
	// выстраивает запросы в очередь и избавляет от ошибки "database is locked".
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil { // Ping реально соединяется с базой — проверяем, что всё живо
		return nil, fmt.Errorf("проверить БД: %w", err)
	}

	s := &Storage{db: db} // собираем нашу обёртку
	if err := s.migrate(); err != nil { // создаём таблицы, если их ещё нет
		return nil, err // migrate уже обернул ошибку текстом
	}
	return s, nil // успех: возвращаем готовое хранилище и nil вместо ошибки
}

// Close закрывает пул соединений. В main вызывается через defer.
func (s *Storage) Close() error { return s.db.Close() }

// migrate создаёт схему. IF NOT EXISTS = безопасно выполнять при каждом запуске.
func (s *Storage) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS submissions (              -- заявки студентов
    id            INTEGER PRIMARY KEY AUTOINCREMENT,  -- автонумерация: 1, 2, 3...
    student_group TEXT    NOT NULL,                   -- номер группы ("111")
    activities    TEXT    NOT NULL,                   -- JSON-массив названий мероприятий
    created_at    INTEGER NOT NULL                    -- Unix-секунды: надёжно, без разборов дат
);
CREATE TABLE IF NOT EXISTS files (                    -- документы к заявкам
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    submission_id INTEGER NOT NULL                    -- ссылка на заявку...
        REFERENCES submissions(id) ON DELETE CASCADE, -- ...удалили заявку → строки файлов уйдут сами
    original_name TEXT    NOT NULL,                   -- имя файла пользователя
    stored_name   TEXT    NOT NULL,                   -- случайное имя на диске
    size_bytes    INTEGER NOT NULL,                   -- размер
    created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS users (                    -- администраторы
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,               -- UNIQUE: двух одинаковых логинов не будет
    password_hash TEXT NOT NULL                       -- только bcrypt-хеш, не пароль!
);
CREATE TABLE IF NOT EXISTS sessions (                 -- «билеты» входа (куки указывают на них)
    token      TEXT    PRIMARY KEY,                   -- случайный 64-символьный токен
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL                       -- Unix-секунды, когда билет протухает
);
CREATE INDEX IF NOT EXISTS idx_files_submission       -- индекс ускоряет поиск файлов заявки
    ON files(submission_id);
`
	if _, err := s.db.Exec(schema); err != nil { // Exec — выполнить SQL без возврата строк
		return fmt.Errorf("создать таблицы: %w", err)
	}
	return nil
}

// EnsureAdmin создаёт администратора, только если пользователей ещё нет (первый запуск).
func (s *Storage) EnsureAdmin(username, password string) error {
	var count int // сюда Scan положит число пользователей
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil { // & — адрес переменной
		return fmt.Errorf("посчитать пользователей: %w", err)
	}
	if count > 0 { // админ уже есть — ничего не делаем (пароль из env НЕ перезаписываем)
		return nil
	}
	// bcrypt сам добавляет случайную «соль» и делает хеш медленным — перебор паролей дорог.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost) // DefaultCost = 10
	if err != nil {
		return fmt.Errorf("хешировать пароль: %w", err)
	}
	// ? — ПАРАМЕТР-заполнитель. Значения подставляются драйвером, а не склейкой строк:
	// это защита от SQL-инъекций. НИКОГДА не пишите fmt.Sprintf("... '%s'", userInput)!
	if _, err := s.db.Exec(`INSERT INTO users (username, password_hash) VALUES (?, ?)`,
		username, string(hash)); err != nil {
		return fmt.Errorf("создать администратора: %w", err)
	}
	return nil
}

// CreateSubmission сохраняет заявку и возвращает её новый номер.
func (s *Storage) CreateSubmission(group string, activities []string) (int64, error) {
	raw, err := json.Marshal(activities) // []string{"ЦОПП"} → строка `["ЦОПП"]` для колонки TEXT
	if err != nil {
		return 0, fmt.Errorf("сериализовать мероприятия: %w", err)
	}
	res, err := s.db.Exec(
		`INSERT INTO submissions (student_group, activities, created_at) VALUES (?, ?, ?)`,
		group, string(raw), time.Now().Unix(), // Unix() — секунды с 01.01.1970, просто число
	)
	if err != nil {
		return 0, fmt.Errorf("сохранить заявку: %w", err)
	}
	return res.LastInsertId() // SQLite сообщает id только что вставленной строки
}

// SubmissionExists проверяет, есть ли заявка с таким номером (перед прикреплением файла).
func (s *Storage) SubmissionExists(id int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM submissions WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) { // «строк нет» — не ошибка, а ответ false
		return false, nil
	}
	if err != nil { // настоящая проблема с базой
		return false, fmt.Errorf("проверить заявку: %w", err)
	}
	return true, nil // строка нашлась
}

// SaveFile записывает метаданные файла. Сам файл к этому моменту уже сохранён на диск.
func (s *Storage) SaveFile(submissionID int64, originalName, storedName string, size int64) error {
	_, err := s.db.Exec(
		`INSERT INTO files (submission_id, original_name, stored_name, size_bytes, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		submissionID, originalName, storedName, size, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("сохранить файл в БД: %w", err)
	}
	return nil
}

// ListSubmissions возвращает все заявки (новые сверху) вместе с их файлами — для админки.
func (s *Storage) ListSubmissions() ([]model.Submission, error) {
	// Query возвращает набор строк (rows). Их читают циклом rows.Next().
	rows, err := s.db.Query(
		`SELECT id, student_group, activities, created_at FROM submissions ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("получить заявки: %w", err)
	}
	defer rows.Close() // обязательно освобождаем rows (соединение у нас всего одно!)

	var subs []model.Submission        // результирующий список (пока nil — пустой)
	index := make(map[int64]int)       // карта: id заявки → её позиция в списке (для файлов ниже)
	for rows.Next() {                  // Next двигает курсор; false = строки кончились
		var sub model.Submission       // пустая структура под текущую строку
		var raw string                 // сюда придёт JSON мероприятий
		var created int64              // сюда — Unix-секунды
		// Scan раскладывает колонки по переменным В ТОМ ЖЕ порядке, что в SELECT.
		if err := rows.Scan(&sub.ID, &sub.Group, &raw, &created); err != nil {
			return nil, fmt.Errorf("прочитать заявку: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &sub.Activities); err != nil { // JSON → []string
			return nil, fmt.Errorf("разобрать мероприятия заявки %d: %w", sub.ID, err)
		}
		sub.CreatedAt = time.Unix(created, 0) // число секунд → нормальный time.Time
		index[sub.ID] = len(subs)             // запоминаем позицию ПЕРЕД добавлением
		subs = append(subs, sub)              // append добавляет элемент в срез
	}
	if err := rows.Err(); err != nil { // Next мог остановиться из-за ошибки — проверяем
		return nil, fmt.Errorf("итерация заявок: %w", err)
	}
	// Цикл выше дошёл до конца → rows закрылись автоматически, соединение свободно.

	// Вторым запросом берём ВСЕ файлы и раскладываем по заявкам (без N+1 запросов).
	frows, err := s.db.Query(
		`SELECT id, submission_id, original_name, stored_name, size_bytes, created_at FROM files`)
	if err != nil {
		return nil, fmt.Errorf("получить файлы: %w", err)
	}
	defer frows.Close() // освободим при выходе из функции
	for frows.Next() {
		var f model.File
		var created int64
		if err := frows.Scan(&f.ID, &f.SubmissionID, &f.OriginalName,
			&f.StoredName, &f.SizeBytes, &created); err != nil {
			return nil, fmt.Errorf("прочитать файл: %w", err)
		}
		f.CreatedAt = time.Unix(created, 0)
		if i, ok := index[f.SubmissionID]; ok { // ok=true, если заявка файла есть в списке
			subs[i].Files = append(subs[i].Files, f) // дописываем файл его заявке
		}
	}
	return subs, frows.Err() // список + возможная ошибка итерации (nil, если всё хорошо)
}

// DeleteSubmission удаляет заявку и возвращает имена её файлов на диске,
// чтобы обработчик потом удалил и сами файлы (БД про диск ничего не знает).
func (s *Storage) DeleteSubmission(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT stored_name FROM files WHERE submission_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("получить файлы заявки: %w", err)
	}
	var names []string // соберём имена до удаления
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close() // закрываем вручную, т.к. выходим раньше конца цикла
			return nil, fmt.Errorf("прочитать имя файла: %w", err)
		}
		names = append(names, name)
	}
	rows.Close() // освобождаем единственное соединение ПЕРЕД следующим запросом
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("итерация файлов: %w", err)
	}

	res, err := s.db.Exec(`DELETE FROM submissions WHERE id = ?`, id) // строки files уйдут по CASCADE
	if err != nil {
		return nil, fmt.Errorf("удалить заявку: %w", err)
	}
	n, _ := res.RowsAffected() // сколько строк реально удалилось
	if n == 0 {                // ноль — значит заявки с таким id не было
		return nil, ErrNotFound
	}
	return names, nil // отдаём имена файлов «на растерзание» обработчику
}

// UserByUsername ищет пользователя по логину (нужно при входе).
func (s *Storage) UserByUsername(username string) (model.User, error) {
	var u model.User
	err := s.db.QueryRow(
		`SELECT id, username, password_hash FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) { // логин не найден — сигнальная ошибка
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("получить пользователя: %w", err)
	}
	return u, nil
}

// CreateSession сохраняет «билет» входа: токен из куки → пользователь + срок годности.
func (s *Storage) CreateSession(token string, userID int64, expires time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expires.Unix(),
	)
	if err != nil {
		return fmt.Errorf("создать сессию: %w", err)
	}
	return nil
}

// SessionUser по токену из куки возвращает пользователя, если сессия жива.
func (s *Storage) SessionUser(token string) (model.User, error) {
	var u model.User
	// JOIN соединяет две таблицы: сессия + её пользователь. Проверяем срок годности.
	err := s.db.QueryRow(`
		SELECT u.id, u.username, u.password_hash
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`,
		token, time.Now().Unix(), // «протухшие» билеты не проходят
	).Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) { // нет билета или он истёк
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("получить сессию: %w", err)
	}
	return u, nil
}

// DeleteSession удаляет билет (выход из системы).
func (s *Storage) DeleteSession(token string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token); err != nil {
		return fmt.Errorf("удалить сессию: %w", err)
	}
	return nil
}

// DeleteExpiredSessions выметает протухшие билеты, чтобы таблица не росла вечно.
func (s *Storage) DeleteExpiredSessions() error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix()); err != nil {
		return fmt.Errorf("очистить сессии: %w", err)
	}
	return nil
}
