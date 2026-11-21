package storage // единственный пакет с SQL

import (
	"crypto/rand"
	"database/sql" // стандартный доступ к SQL
	"encoding/hex"
	"errors"        // сигнальные ошибки
	"fmt"           // оборачивание ошибок, плейсхолдеры
	"os"            // папка базы
	"path/filepath" // пути
	"strings"       // сборка IN (...)
	"student-activity/internal/model"
	"time" // Unix-время

	"golang.org/x/crypto/bcrypt" // хеш пароля админа

	_ "modernc.org/sqlite" // регистрация драйвера "sqlite"
)

// Сигнальные ошибки: обработчики различают их через errors.Is.
var (
	ErrNotFound  = errors.New("запись не найдена")
	ErrRoomTaken = errors.New("аудитория уже занята в это время")
	ErrDecided   = errors.New("заявка уже обработана")
)

type Storage struct{ db *sql.DB } // обёртка над пулом соединений

func New(dbPath string) (*Storage, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil { // создаём data/
		return nil, fmt.Errorf("папка БД: %w", err)
	}
	// _pragma — параметры драйвера modernc: ждём блокировку, WAL, внешние ключи.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn) // Open ленивый — соединение не открывается сразу
	if err != nil {
		return nil, fmt.Errorf("открыть БД: %w", err)
	}
	db.SetMaxOpenConns(1)             // SQLite — один писатель; одно соединение = нет "database is locked"
	if err := db.Ping(); err != nil { // реальная проверка соединения
		return nil, fmt.Errorf("ping БД: %w", err)
	}
	s := &Storage{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Storage) Close() error { return s.db.Close() }

func (s *Storage) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,          -- логин уникален
    password_hash TEXT NOT NULL,                 -- только bcrypt-хеш
    role          TEXT NOT NULL,                 -- admin/teacher/student
    full_name     TEXT NOT NULL DEFAULT '',      -- ФИО преподавателя
    group_number  TEXT NOT NULL DEFAULT '',      -- группа студента
    ical_hash     TEXT NOT NULL UNIQUE,          -- секрет календаря
    created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS user_activities (     -- доп. активности студента
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity TEXT NOT NULL,
    PRIMARY KEY (user_id, activity)              -- без дублей
);
CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS lessons (             -- расписание (CSV + одобренные заявки)
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    csv_id        TEXT NOT NULL UNIQUE,          -- ключ повторного импорта
    prepod_name   TEXT NOT NULL,
    group_number  TEXT NOT NULL,                 -- группа или доп. активность
    title         TEXT NOT NULL,
    place         TEXT NOT NULL,
    day           INTEGER NOT NULL,              -- 1..6
    lesson_number INTEGER NOT NULL,              -- пара 1..6
    week_type     TEXT NOT NULL,                 -- all/num/den
    type          TEXT NOT NULL,
    sequence      INTEGER NOT NULL DEFAULT 0,    -- iCal SEQUENCE: +1 при каждом изменении
    source        TEXT NOT NULL DEFAULT 'csv',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lessons_group ON lessons(group_number);
CREATE INDEX IF NOT EXISTS idx_lessons_slot  ON lessons(day, lesson_number);
CREATE TABLE IF NOT EXISTS rooms (               -- все аудитории (из CSV и заявок)
    name TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS requests (            -- заявки преподавателей
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    teacher_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    target        TEXT NOT NULL,                 -- группа или доп. активность
    day           INTEGER NOT NULL,
    lesson_number INTEGER NOT NULL,
    week_type     TEXT NOT NULL,
    place         TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    created_at    INTEGER NOT NULL,
    decided_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_requests_status ON requests(status);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("схема: %w", err)
	}
	return nil
}

// EnsureAdmin: если админ уже есть — НИЧЕГО не делаем; иначе создаём из .env.
func (s *Storage) EnsureAdmin(username, password string) error {
	var n int
	// Важно: считаем именно АДМИНОВ, а не всех пользователей (студенты регистрируются сами).
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&n); err != nil {
		return fmt.Errorf("подсчёт админов: %w", err)
	}
	if n > 0 {
		return nil // админ уже в базе — пароль из .env его не перезаписывает
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost) // соль+медленный хеш
	if err != nil {
		return fmt.Errorf("хеш пароля: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO users (username, password_hash, role, ical_hash, created_at) VALUES (?, ?, 'admin', ?, ?)`,
		username, string(hash), randomHash(), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("создать админа: %w", err)
	}
	return nil
}

// CreateUser создаёт пользователя любой роли (студент — сам, преподаватель — админ).
func (s *Storage) CreateUser(username, passHash, role, fullName string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, role, full_name, ical_hash, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		username, passHash, role, fullName, randomHash(), time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("создать пользователя: %w", err)
	}
	return res.LastInsertId()
}

const userCols = `id, username, password_hash, role, full_name, group_number, ical_hash, created_at`

func scanUser(row interface{ Scan(...any) error }) (model.User, error) { // общий скан для Row/Rows
	var u model.User
	var created int64
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.FullName,
		&u.GroupNumber, &u.IcalHash, &created)
	u.CreatedAt = time.Unix(created, 0)
	return u, err
}

func (s *Storage) UserByUsername(username string) (model.User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ?`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return u, err
}

func (s *Storage) UserByIcalHash(hash string) (model.User, error) { // поиск по секрету календаря
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE ical_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return u, err
}

func (s *Storage) ListTeachers() ([]model.User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users WHERE role = 'teacher' ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetStudentProfile сохраняет группу и доп. активности студента одной транзакцией.
func (s *Storage) SetStudentProfile(userID int64, group string, activities []string) error {
	tx, err := s.db.Begin() // транзакция: всё или ничего
	if err != nil {
		return err
	}
	defer tx.Rollback() // откат, если Commit не случится (после Commit — безвреден)
	if _, err := tx.Exec(`UPDATE users SET group_number = ? WHERE id = ?`, group, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_activities WHERE user_id = ?`, userID); err != nil {
		return err // перезаписываем набор активностей целиком
	}
	for _, a := range activities {
		if _, err := tx.Exec(`INSERT INTO user_activities (user_id, activity) VALUES (?, ?)`, userID, a); err != nil {
			return err
		}
	}
	return tx.Commit() // фиксируем изменения
}

func (s *Storage) StudentActivities(userID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT activity FROM user_activities WHERE user_id = ? ORDER BY activity`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- Сессии (как в прошлой версии) ----------

func (s *Storage) CreateSession(token string, userID int64, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expires.Unix())
	return err
}

func (s *Storage) SessionUser(token string) (model.User, error) {
	row := s.db.QueryRow(`SELECT `+userCols+` FROM users u
		JOIN sessions s ON s.user_id = u.id
		WHERE s.token = ? AND s.expires_at > ?`, token, time.Now().Unix())
	// В JOIN имена колонок уточняем префиксом, иначе "ambiguous column name".
	row = s.db.QueryRow(`SELECT u.id, u.username, u.password_hash, u.role, u.full_name,
		u.group_number, u.ical_hash, u.created_at FROM users u
		JOIN sessions s ON s.user_id = u.id
		WHERE s.token = ? AND s.expires_at > ?`, token, time.Now().Unix())
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return u, err
}

func (s *Storage) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Storage) DeleteExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

// ---------- Расписание ----------

const lessonCols = `id, csv_id, prepod_name, group_number, title, place,
	day, lesson_number, week_type, type, sequence, source, created_at, updated_at`

func scanLesson(row interface{ Scan(...any) error }) (model.Lesson, error) {
	var l model.Lesson
	var created, updated int64
	err := row.Scan(&l.ID, &l.CsvID, &l.PrepodName, &l.GroupNumber, &l.Title, &l.Place,
		&l.Day, &l.Pair, &l.WeekType, &l.Type, &l.Sequence, &l.Source, &created, &updated)
	l.CreatedAt = time.Unix(created, 0)
	l.UpdatedAt = time.Unix(updated, 0)
	return l, err
}

// ImportLessons — UPSERT по csv_id: новое → вставка; изменилось → UPDATE и sequence+1.
func (s *Storage) ImportLessons(lessons []model.Lesson) (added, updated, unchanged int, err error) {
	tx, err := s.db.Begin() // весь импорт — одна транзакция (атомарно и быстро)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, l := range lessons {
		old, err := scanLesson(tx.QueryRow(`SELECT `+lessonCols+` FROM lessons WHERE csv_id = ?`, l.CsvID))
		if errors.Is(err, sql.ErrNoRows) { // такой строки ещё нет → вставляем
			if _, err := tx.Exec(`INSERT INTO lessons
				(csv_id, prepod_name, group_number, title, place, day, lesson_number,
				 week_type, type, sequence, source, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 'csv', ?, ?)`,
				l.CsvID, l.PrepodName, l.GroupNumber, l.Title, l.Place, l.Day, l.Pair,
				l.WeekType, l.Type, now, now); err != nil {
				return 0, 0, 0, fmt.Errorf("вставка %s: %w", l.CsvID, err)
			}
			added++
		} else if err != nil {
			return 0, 0, 0, err
		} else { // строка есть — сравниваем содержимое
			same := old.PrepodName == l.PrepodName && old.GroupNumber == l.GroupNumber &&
				old.Title == l.Title && old.Place == l.Place && old.Day == l.Day &&
				old.Pair == l.Pair && old.WeekType == l.WeekType && old.Type == l.Type
			if same {
				unchanged++ // не трогаем — SEQUENCE не растёт зря
				continue
			}
			// Событие изменилось: SEQUENCE+1 — календарные клиенты поймут, что это обновление.
			if _, err := tx.Exec(`UPDATE lessons SET prepod_name=?, group_number=?, title=?,
				place=?, day=?, lesson_number=?, week_type=?, type=?,
				sequence = sequence + 1, updated_at = ? WHERE id = ?`,
				l.PrepodName, l.GroupNumber, l.Title, l.Place, l.Day, l.Pair,
				l.WeekType, l.Type, now, old.ID); err != nil {
				return 0, 0, 0, fmt.Errorf("обновление %s: %w", l.CsvID, err)
			}
			updated++
		}
		// Каждая аудитория из CSV попадает в справочник rooms (INSERT OR IGNORE — без дублей).
		if _, err := tx.Exec(`INSERT OR IGNORE INTO rooms (name) VALUES (?)`, l.Place); err != nil {
			return 0, 0, 0, err
		}
	}
	return added, updated, unchanged, tx.Commit()
}

// LessonsForUser — расписание студента: его группа + выбранные доп. активности.
func (s *Storage) LessonsForUser(u model.User) ([]model.Lesson, error) {
	acts, err := s.StudentActivities(u.ID)
	if err != nil {
		return nil, err
	}
	var where []string // собираем условия динамически
	var args []any
	if u.GroupNumber != "" {
		where = append(where, "group_number = ?")
		args = append(args, u.GroupNumber)
	}
	if len(acts) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(acts)), ",") // "?,?,?" по числу активностей
		where = append(where, "group_number IN ("+ph+")")
		for _, a := range acts {
			args = append(args, a)
		}
	}
	if len(where) == 0 { // профиль ещё не заполнен — календарь пуст
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT `+lessonCols+` FROM lessons WHERE `+
		strings.Join(where, " OR ")+` ORDER BY day, lesson_number`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Lesson
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Groups — все «числовые» группы из расписания (доп. активности — не числа).
func (s *Storage) Groups() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT group_number FROM lessons ORDER BY group_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		if isNumeric(g) { // числовые — группы, остальные — доп. группы
			out = append(out, g)
		}
	}
	return out, rows.Err()
}

// ExtraGroups — нечисловые «группы» из расписания (например, одобренные лекции для ЦОПП).
func (s *Storage) ExtraGroups() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT group_number FROM lessons ORDER BY group_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		if !isNumeric(g) {
			out = append(out, g)
		}
	}
	return out, rows.Err()
}

func isNumeric(s string) bool { // вся строка — цифры?
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Storage) Rooms() ([]string, error) { // все аудитории
	rows, err := s.db.Query(`SELECT name FROM rooms ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// occupancyCond — условие «занято в этом слоте». Недели пересекаются так:
// all занимает обе; num конфликтует с all и num; den — с all и den.
func occupancyCond(week string) (string, []any) {
	switch week {
	case model.WeekNum:
		return " AND week_type IN ('all','num')", nil
	case model.WeekDen:
		return " AND week_type IN ('all','den')", nil
	default: // all конфликтует с любыми
		return "", nil
	}
}

// FreeRooms — аудитории, НЕ занятые в заданные день/пару/неделю.
func (s *Storage) FreeRooms(day, pair int, week string) ([]string, error) {
	rooms, err := s.Rooms()
	if err != nil {
		return nil, err
	}
	cond, extra := occupancyCond(week)
	args := append([]any{day, pair}, extra...)
	rows, err := s.db.Query(`SELECT DISTINCT place FROM lessons
		WHERE day = ? AND lesson_number = ?`+cond, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	busy := make(map[string]bool) // множество занятых
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		busy[p] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var free []string
	for _, r := range rooms {
		if !busy[r] {
			free = append(free, r)
		}
	}
	return free, nil
}

// RoomFree — точечная проверка перед одобрением заявки (защита от двойного бронирования).
func (s *Storage) roomFreeTx(tx *sql.Tx, day, pair int, week, room string) (bool, error) {
	cond, extra := occupancyCond(week)
	args := append([]any{day, pair, room}, extra...)
	var one int
	err := tx.QueryRow(`SELECT 1 FROM lessons
		WHERE day = ? AND lesson_number = ? AND place = ?`+cond+` LIMIT 1`, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil // никто не занимает
	}
	if err != nil {
		return false, err
	}
	return false, nil // найдена占用 строка
}

func (s *Storage) RoomFree(day, pair int, week, room string) (bool, error) { // обёртка без транзакции
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	return s.roomFreeTx(tx, day, pair, week, room)
}

// ---------- Заявки преподавателей ----------

const reqCols = `r.id, r.teacher_id, COALESCE(NULLIF(u.full_name,''), u.username),
	r.title, r.target, r.day, r.lesson_number, r.week_type, r.place, r.status, r.created_at`

func scanRequest(row interface{ Scan(...any) error }) (model.LectureRequest, error) {
	var q model.LectureRequest
	var created int64
	err := row.Scan(&q.ID, &q.TeacherID, &q.TeacherName, &q.Title, &q.Target,
		&q.Day, &q.Pair, &q.WeekType, &q.Place, &q.Status, &created)
	q.CreatedAt = time.Unix(created, 0)
	return q, err
}

func (s *Storage) CreateRequest(q model.LectureRequest) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO requests
		(teacher_id, title, target, day, lesson_number, week_type, place, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
		q.TeacherID, q.Title, q.Target, q.Day, q.Pair, q.WeekType, q.Place, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Storage) ListRequests(teacherID int64) ([]model.LectureRequest, error) { // 0 = все (админ)
	query := `SELECT ` + reqCols + ` FROM requests r JOIN users u ON u.id = r.teacher_id`
	var args []any
	if teacherID > 0 {
		query += ` WHERE r.teacher_id = ?`
		args = append(args, teacherID)
	}
	query += ` ORDER BY r.id DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.LectureRequest
	for rows.Next() {
		q, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// DecideRequest — одобрить/отклонить. Одобрение В ТОЙ ЖЕ транзакции:
// перепроверяет аудиторию, создаёт занятие в расписании, добавляет аудиторию в rooms.
func (s *Storage) DecideRequest(id int64, approve bool, teacherName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q, err := scanRequest(tx.QueryRow(`SELECT `+reqCols+` FROM requests r
		JOIN users u ON u.id = r.teacher_id WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if q.Status != "pending" { // уже обработана — повторно нельзя
		return ErrDecided
	}
	now := time.Now().Unix()
	if approve {
		free, err := s.roomFreeTx(tx, q.Day, q.Pair, q.WeekType, q.Place) // повторная проверка!
		if err != nil {
			return err
		}
		if !free {
			return ErrRoomTaken // другую заявку с этой аудиторией одобрили раньше
		}
		// Заявка становится занятием: аудитория занята на эту пару по времени,
		// привязка — к группе или доп. группе (target). SEQUENCE нового события = 0.
		if _, err := tx.Exec(`INSERT INTO lessons
			(csv_id, prepod_name, group_number, title, place, day, lesson_number,
			 week_type, type, sequence, source, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'лекция', 0, 'request', ?, ?)`,
			fmt.Sprintf("req-%d", q.ID), q.TeacherName, q.Target, q.Title, q.Place,
			q.Day, q.Pair, q.WeekType, now, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO rooms (name) VALUES (?)`, q.Place); err != nil {
			return err
		}
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	if _, err := tx.Exec(`UPDATE requests SET status = ?, decided_at = ? WHERE id = ?`,
		status, now, id); err != nil {
		return err
	}
	return tx.Commit() // всё вместе зафиксировано
}

func randomHash() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil { // криптографическая случайность
		panic(err)
	}
	return hex.EncodeToString(b) // 32 hex-символа
}
