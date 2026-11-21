package model // сущности и справочники, без логики БД/HTTP

import (
	"fmt"  // подписи пар
	"time" // время
)

// ---------- Роли ----------
const (
	RoleAdmin   = "admin"   // управляет заявками, CSV, преподавателями
	RoleTeacher = "teacher" // подаёт заявки на лекции
	RoleStudent = "student" // выбирает группу/активности, получает календарь
)

// ---------- Типы недель (числитель/знаменатель) ----------
const (
	WeekAll = "all" // каждую неделю
	WeekNum = "num" // только числитель
	WeekDen = "den" // только знаменатель
)

func WeekLabel(w string) string { // человеческая подпись недели
	switch w {
	case WeekNum:
		return "числитель"
	case WeekDen:
		return "знаменатель"
	default:
		return "каждая неделя"
	}
}

// ---------- Сетка звонков (пары). Меняется под ваш вуз в одном месте. ----------
var PairNumbers = []int{1, 2, 3, 4, 5, 6} // порядок для выпадающих списков (map неупорядочен!)
var PairTimes = map[int][2]string{        // номер пары → [начало, конец]
	1: {"09:00", "10:30"},
	2: {"10:40", "12:10"},
	3: {"12:40", "14:10"},
	4: {"14:20", "15:50"},
	5: {"16:00", "17:30"},
	6: {"17:40", "19:10"},
}

func PairLabel(n int) string { // подпись для селекта: "3 пара (12:40–14:10)"
	t := PairTimes[n]
	return fmt.Sprintf("%d пара (%s–%s)", n, t[0], t[1])
}

// ---------- Дни недели ----------
var DayNumbers = []int{1, 2, 3, 4, 5, 6} // 1=Пн ... 6=Сб
var WeeksNumbers = []int{1, 2}           // 1=Пн ... 6=Сб
var Weeks = map[int]string{1: "Числитель", 2: "Знаменатель"}
var DayNames = map[int]string{1: "Понедельник", 2: "Вторник", 3: "Среда",
											4: "Четверг", 5: "Пятница", 6: "Суббота"}
var DayCodes = map[int]string{1: "MO", 2: "TU", 3: "WE", 4: "TH", 5: "FR", 6: "SA"} // для RRULE BYDAY

// ---------- Доп. активности (доп. группы из вашего index.js) ----------
var Activities = []string{"ЦОПП", "Лекции студенческого клуба разработки"}

// Запасной список групп, пока CSV не загружен (из вашего index.js).
var FallbackGroups = []string{"111", "121", "131", "132", "141", "151", "181"}

// ---------- Сущности ----------

type User struct {
	ID           int64  // номер
	Username     string // логин (уникален)
	PasswordHash string // bcrypt-хеш
	Role         string // admin / teacher / student
	FullName     string // ФИО преподавателя (для заявок и расписания)
	GroupNumber  string // группа студента
	IcalHash     string // секрет календаря: /ical/<hash>.ics и пароль CalDAV
	CreatedAt    time.Time
}

type Lesson struct {
	ID          int64  // номер строки расписания (из него делаем стабильный UID)
	CsvID       string // id из CSV (ключ для повторного импорта) или "req-N"
	PrepodName  string // преподаватель
	GroupNumber string // группа ИЛИ название доп. активности
	Title       string // название предмета (колонка lesson)
	Place       string // аудитория (колонка place)
	Day         int    // день недели 1..6
	Pair        int    // номер пары 1..6
	WeekType    string // all / num / den
	Type        string // тип занятия (лекция/практика/...)
	Sequence    int    // счётчик изменений события (iCal SEQUENCE)
	Source      string // "csv" или "request"
	CreatedAt   time.Time
	UpdatedAt   time.Time // меняется при каждом изменении → DTSTAMP/ETag
}

type LectureRequest struct {
	ID          int64  // номер заявки
	TeacherID   int64  // кто подал
	TeacherName string // ФИО/логин (из JOIN)
	Title       string // название лекции
	Target      string // группа или доп. активность
	Day         int    // день
	Pair        int    // пара
	WeekType    string // неделя
	Place       string // выбранная аудитория
	Status      string // pending / approved / rejected
	CreatedAt   time.Time
}
