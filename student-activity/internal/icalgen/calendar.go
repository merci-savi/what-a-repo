package icalgen // генерация iCal через github.com/emersion/go-ical

import (
	"bytes"   // буфер в памяти: сюда кодируем .ics
	"fmt"     // сборка строк UID и RRULE
	"strconv" // число → строка (нужно для SEQUENCE)
	"strings" // склейка описания события
	"time"    // даты, время, часовой пояс

	"github.com/emersion/go-ical" // структуры VCALENDAR/VEVENT и кодировщик

	"student-activity/internal/model" // наши Lesson/User и справочники

	_ "time/tzdata" // встраивает базу часовых поясов в бинарник (важно для Windows)
)

// ВАЖНО: ниже имена компонентов и свойств записаны СТРОКАМИ ("VEVENT", "DTSTART"…).
// Так требует RFC 5545, и так работает любая версия go-ical: константы библиотеки
// (ical.CompEvent, ical.PropTransp и т.п.) — лишь удобство, и есть они не для всех имён.

const tzID = "Europe/Moscow" // требуемый часовой пояс

var moscow = mustLocation() // загружаем пояс один раз при старте пакета

func mustLocation() *time.Location {
	loc, err := time.LoadLocation(tzID) // благодаря time/tzdata работает на любой ОС
	if err != nil {
		panic(err) // без пояса календарь неверен — лучше упасть сразу при старте
	}
	return loc
}

type Generator struct {
	Host  string    // домен для UID (стабильные идентификаторы событий)
	Start time.Time // начало семестра — якорь недели «числитель»
	End   time.Time // конец семестра — граница UNTIL в RRULE
}

func New(host string, start, end time.Time) *Generator { // конструктор генератора
	return &Generator{Host: host, Start: start, End: end}
}

// vtimezone — блок VTIMEZONE для Europe/Moscow.
// Москва с 2014 года живёт в UTC+3 без летнего времени, поэтому хватает
// одного подкомпонента STANDARD с постоянным смещением.
func vtimezone() *ical.Component {
	tz := ical.NewComponent("VTIMEZONE")                // BEGIN:VTIMEZONE
	tz.Props.SetText("TZID", tzID)                      // TZID:Europe/Moscow
	std := ical.NewComponent("STANDARD")                // BEGIN:STANDARD (подкомпонент пояса)
	setRawProp(std.Props, "DTSTART", "19700101T000000") // правило действует «с всегда» (DATE-TIME)
	setRawProp(std.Props, "TZOFFSETFROM", "+0300")      // смещение до перехода (UTC-OFFSET)
	setRawProp(std.Props, "TZOFFSETTO", "+0300")        // смещение после (то же самое)
	std.Props.SetText("TZNAME", "MSK")                  // короткое имя пояса (тип TEXT — по умолчанию)
	tz.Children = append(tz.Children, std)              // вкладываем STANDARD в VTIMEZONE
	return tz
}

// setRawProp кладёт свойство БЕЗ параметра VALUE: тип берётся тот, что по
// умолчанию в RFC 5545 (RRULE → RECUR, SEQUENCE → INTEGER, TZOFFSET* → UTC-OFFSET).
// Нужен там, где SetText поставил бы неверный VALUE=TEXT: с таким значением
// строгие клиенты и сам go-webdav (разбор RRULE в REPORT) отвергают файл.
func setRawProp(props ical.Props, name, value string) {
	p := ical.NewProp(name) // без Params → VALUE не печатается в .ics
	p.Value = value
	props.Set(p)
}

// setLocalDateTime ставит DTSTART/DTEND как локальное время С параметром TZID:
// DTSTART;TZID=Europe/Moscow:20260901T090000 — именно так ждут календарные клиенты.
func setLocalDateTime(c *ical.Component, name string, t time.Time) {
	p := &ical.Prop{Name: name, Params: ical.Params{}} // создаём свойство вручную
	p.Params.Set("TZID", tzID)                         // параметр TZID=Europe/Moscow
	p.Value = t.Format("20060102T150405")              // локальный формат, БЕЗ суффикса Z
	c.Props.Set(p)                                     // кладём свойство в компонент
}

// firstSlot вычисляет ПЕРВОЕ занятие пары в семестре с учётом числителя/знаменателя.
// Правило: неделя якоря (SEMESTER_START) — числитель, дальше чередование через неделю.
func (g *Generator) firstSlot(l model.Lesson) (time.Time, time.Time) {
	anchor := time.Date(g.Start.Year(), g.Start.Month(), g.Start.Day(), 0, 0, 0, 0, moscow)
	wd := int(anchor.Weekday()) // воскресенье=0 → приводим к ISO (Пн=1 … Вс=7)
	if wd == 0 {
		wd = 7
	}
	monday := anchor.AddDate(0, 0, -(wd - 1)) // понедельник недели якоря
	day := monday.AddDate(0, 0, l.Day-1)      // нужный день недели на той же неделе
	if day.Before(anchor) {                   // этот день уже прошёл до старта семестра?
		day = day.AddDate(0, 0, 7) // тогда берём следующую неделю
	}
	// Номер недели от якоря: чётный = числитель, нечётный = знаменатель.
	weekIdx := int(day.Sub(monday)/(24*time.Hour)) / 7 // в Москве нет DST — деление безопасно
	need := 0                                          // num → чётная неделя (остаток 0)
	if l.WeekType == model.WeekDen {
		need = 1 // den → нечётная неделя (остаток 1)
	}
	if l.WeekType != model.WeekAll && weekIdx%2 != need {
		day = day.AddDate(0, 0, 7) // сдвиг на неделю, чтобы попасть в свою чётность
	}
	// Накладываем время пары из сетки звонков.
	startHM, _ := time.Parse("15:04", model.PairTimes[l.Pair][0]) // "09:00" → часы и минуты
	endHM, _ := time.Parse("15:04", model.PairTimes[l.Pair][1])
	start := time.Date(day.Year(), day.Month(), day.Day(), startHM.Hour(), startHM.Minute(), 0, 0, moscow)
	end := time.Date(day.Year(), day.Month(), day.Day(), endHM.Hour(), endHM.Minute(), 0, 0, moscow)
	return start, end
}

// Event строит VEVENT одного занятия.
func (g *Generator) Event(l model.Lesson) *ical.Component {
	ev := ical.NewComponent("VEVENT") // BEGIN:VEVENT
	// UID СТАБИЛЕН (номер строки БД): при обновлении клиент меняет событие, а не плодит дубли.
	ev.Props.SetText("UID", fmt.Sprintf("lesson-%d@%s", l.ID, g.Host))
	// DTSTAMP — момент последнего изменения занятия (из БД), а НЕ time.Now():
	// иначе содержимое .ics менялось бы при каждом запросе и ETag «плавал» бы —
	// клиент считал бы все события изменёнными и заново перекачивал календарь.
	dtstamp := l.UpdatedAt.UTC()
	if dtstamp.IsZero() {
		dtstamp = time.Now().UTC() // страховка: если UpdatedAt не заполнен
	}
	ev.Props.SetDateTime("DTSTAMP", dtstamp) // DTSTAMP:...Z
	start, end := g.firstSlot(l)             // первая пара с учётом чётности недели
	setLocalDateTime(ev, "DTSTART", start)   // DTSTART;TZID=Europe/Moscow:...
	setLocalDateTime(ev, "DTEND", end)       // DTEND;TZID=Europe/Moscow:...
	// SEQUENCE по стандарту — целое число (тип INTEGER по умолчанию, VALUE не печатается).
	// Метода SetInt в библиотеке нет, поэтому число превращаем в строку сами.
	setRawProp(ev.Props, "SEQUENCE", strconv.Itoa(l.Sequence)) // 0, 1, 2… — растёт при изменениях
	ev.Props.SetText("SUMMARY", l.Title+" ("+l.Type+")")       // заголовок события
	ev.Props.SetText("LOCATION", l.Place)                      // аудитория
	ev.Props.SetText("DESCRIPTION", strings.Join([]string{     // описание в несколько строк
		"Преподаватель: " + l.PrepodName,
		"Группа/активность: " + l.GroupNumber,
		"Неделя: " + model.WeekLabel(l.WeekType),
		fmt.Sprintf("Пара №%d", l.Pair),
	}, "\n"))
	// RRULE — повторение. Пары ПО НЕДЕЛЯМ: INTERVAL=2 для числителя/знаменателя,
	// INTERVAL=1 для «каждую неделю». UNTIL — конец семестра в UTC (суффикс Z).
	interval := 1
	if l.WeekType != model.WeekAll {
		interval = 2
	}
	until := time.Date(g.End.Year(), g.End.Month(), g.End.Day(), 23, 59, 59, 0, time.UTC)
	// Тип свойства RRULE — RECUR (по умолчанию в RFC 5545). Через SetText был бы
	// VALUE=TEXT, и тогда go-webdav не смог бы развернуть правило в REPORT → клиент
	// получил бы пустой календарь.
	setRawProp(ev.Props, "RRULE", fmt.Sprintf(
		"FREQ=WEEKLY;INTERVAL=%d;BYDAY=%s;UNTIL=%s",
		interval, model.DayCodes[l.Day], until.Format("20060102T150405Z")))
	ev.Props.SetText("STATUS", "CONFIRMED") // событие подтверждено
	ev.Props.SetText("TRANSP", "OPAQUE")    // время считается занятым (busy)
	return ev
}

// Calendar собирает целый VCALENDAR: шапка + VTIMEZONE + все события.
func (g *Generator) Calendar(u model.User, lessons []model.Lesson, feed bool) *ical.Calendar {
	cal := ical.NewCalendar()                                         // пустой VCALENDAR
	cal.Props.SetText("VERSION", "2.0")                               // VERSION:2.0
	cal.Props.SetText("PRODID", "-//StudentActivity//Schedule//RU")   // PRODID — кто сгенерировал
	cal.Props.SetText("CALSCALE", "GREGORIAN")                        // григорианский календарь
	setRawProp(cal.Props, "X-WR-CALNAME", "Расписание — "+u.Username) // имя календаря в клиенте
	setRawProp(cal.Props, "X-WR-TIMEZONE", tzID)                      // подсказка о поясе клиенту
	if feed {
		cal.Props.SetText("METHOD", "PUBLISH") // METHOD:PUBLISH — для ленты подписки
	}
	cal.Children = append(cal.Children, vtimezone()) // обязательный блок VTIMEZONE
	for _, l := range lessons {
		cal.Children = append(cal.Children, g.Event(l)) // по событию на каждое занятие
	}
	return cal
}

// Object — календарь с ОДНИМ событием: так CalDAV хранит объекты (файл = событие).
func (g *Generator) Object(u model.User, l model.Lesson) *ical.Calendar {
	return g.Calendar(u, []model.Lesson{l}, false) // без METHOD — это не лента
}

// Encode сериализует календарь в текст .ics.
func Encode(cal *ical.Calendar) ([]byte, error) {
	var buf bytes.Buffer                                      // пишем в память, не сразу в сеть
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil { // кодируем по RFC 5545
		return nil, err
	}
	return buf.Bytes(), nil // готовые байты .ics
}
