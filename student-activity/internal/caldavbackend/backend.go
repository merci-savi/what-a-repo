package caldavbackend // CalDAV-бэкенд поверх нашей SQLite (версия под go-webdav v0.7+)

import (
	"context"     // контекст запроса: несёт авторизованного пользователя
	"crypto/sha1" // хеш содержимого для ETag
	"errors"      // тексты ошибок
	"fmt"         // форматирование строк
	"net/http"    // коды статусов (403, 404, 401)
	"path"        // работа с URL-путями (Clean/Dir/Base)
	"strconv"     // число из имени файла
	"strings"     // TrimPrefix/TrimSuffix

	"github.com/emersion/go-ical"          // тип *ical.Calendar в сигнатуре Put
	"github.com/emersion/go-webdav"        // NewHTTPError — ошибка с HTTP-кодом
	"github.com/emersion/go-webdav/caldav" // интерфейс Backend и типы

	"student-activity/internal/icalgen" // генератор событий
	"student-activity/internal/model"   // User/Lesson
	"student-activity/internal/storage" // база данных
)

// UserKey — ключ контекста: auth-middleware кладёт сюда model.User,
// а бэкенд достаёт. Пустая структура не занимает памяти.
type UserKey struct{}

type Backend struct {
	Store  *storage.Storage   // источник расписания
	Gen    *icalgen.Generator // генератор iCal
	Prefix string             // "/caldav/"
}

// Та самая строка-проверка: компилятор сверит ВСЕ методы интерфейса.
// Именно она поймала вашу ошибку — это полезно, а не вредно.
var _ caldav.Backend = (*Backend)(nil)

// user достаёт пользователя из контекста (его положил caldavBasicAuth).
func (b *Backend) user(ctx context.Context) (model.User, error) {
	u, ok := ctx.Value(UserKey{}).(model.User) // утверждение типа: ctx.Value → model.User
	if !ok {                                   // пользователя нет = нет авторизации
		return model.User{}, webdav.NewHTTPError(http.StatusUnauthorized, errors.New("нет авторизации"))
	}
	return u, nil
}

// principal — URL пользователя («кто я»). ОТЛИЧАЕТСЯ от home-set:
// если сделать их одинаковыми, обработчик ответит свойствами принципала
// вместо списка календарей, и клиенты не найдут календарь.
func (b *Backend) principal(u model.User) string { return b.Prefix + u.Username + "/" }

// homeSet — папка календарей пользователя (calendar-home-set по RFC 4791).
func (b *Backend) homeSet(u model.User) string { return b.principal(u) + "calendars/" }

// calPath — единственный календарь пользователя.
func (b *Backend) calPath(u model.User) string { return b.homeSet(u) + "schedule/" }

// checkPath проверяет, что запрошенный путь — именно календарь ТЕКУЩЕГО пользователя.
func (b *Backend) checkPath(ctx context.Context, p string) (model.User, error) {
	u, err := b.user(ctx)
	if err != nil {
		return u, err
	}
	// Clean убирает лишние слэши; сравниваем нормализованные пути.
	if path.Clean(p) != path.Clean(b.calPath(u)) {
		return u, webdav.NewHTTPError(http.StatusNotFound, errors.New("календарь не найден"))
	}
	return u, nil
}

// CurrentUserPrincipal — НОВЫЙ обязательный метод (из webdav.UserPrincipalBackend).
// Клиент спрашивает «кто я?» — отвечаем URL принципала.
func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	u, err := b.user(ctx)
	if err != nil {
		return "", err
	}
	return b.principal(u), nil
}

// CalendarHomeSetPath — где лежат календари пользователя.
func (b *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	u, err := b.user(ctx)
	if err != nil {
		return "", err
	}
	return b.homeSet(u), nil
}

// CreateCalendar — ИСПРАВЛЕННАЯ сигнатура: теперь приходит *caldav.Calendar
// (внутри Path, Name, Description), а не просто строка-путь.
// Нам параметр не нужен (мы всё равно отказываем), поэтому имя "_":
// в Go неиспользуемые ПАРАМЕТРЫ разрешены (в отличие от локальных переменных).
func (b *Backend) CreateCalendar(ctx context.Context, _ *caldav.Calendar) error {
	return webdav.NewHTTPError(http.StatusForbidden, errors.New("календарь создаётся автоматически"))
}

// calendar описывает единственный календарь пользователя.
func (b *Backend) calendar(u model.User) caldav.Calendar {
	return caldav.Calendar{
		Path:                  b.calPath(u),
		Name:                  "Расписание — " + u.Username,
		Description:           "Группа " + u.GroupNumber + " и доп. активности (только чтение)",
		SupportedComponentSet: []string{"VEVENT"}, // поддерживаем только события
	}
}

func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	u, err := b.user(ctx)
	if err != nil {
		return nil, err
	}
	return []caldav.Calendar{b.calendar(u)}, nil // календарь ровно один
}

func (b *Backend) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	u, err := b.checkPath(ctx, p) // чужой путь → 404
	if err != nil {
		return nil, err
	}
	c := b.calendar(u)
	return &c, nil
}

// object строит CalDAV-объект: один .ics = одно событие + ETag + время изменения.
func (b *Backend) object(u model.User, l model.Lesson) (*caldav.CalendarObject, error) {
	cal := b.Gen.Object(u, l) // VCALENDAR с одним VEVENT (и VTIMEZONE)
	raw, err := icalgen.Encode(cal)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(raw) // ETag = хеш содержимого: событие изменилось → ETag другой
	return &caldav.CalendarObject{
		Path:          b.calPath(u) + fmt.Sprintf("lesson-%d.ics", l.ID),
		ModTime:       l.UpdatedAt,              // свежесть по updated_at (SEQUENCE внутри)
		ContentLength: int64(len(raw)),          // новое поле: размер в байтах
		ETag:          fmt.Sprintf(`"%x"`, sum), // ETag в кавычках по HTTP-спецификации
		Data:          cal,
	}, nil
}

func (b *Backend) ListCalendarObjects(ctx context.Context, p string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	u, err := b.checkPath(ctx, p)
	if err != nil {
		return nil, err
	}
	lessons, err := b.Store.LessonsForUser(u) // группа + активности, свежие из БД
	if err != nil {
		return nil, err
	}
	var out []caldav.CalendarObject
	for _, l := range lessons {
		obj, err := b.object(u, l)
		if err != nil {
			return nil, err
		}
		out = append(out, *obj)
	}
	return out, nil
}

func (b *Backend) GetCalendarObject(ctx context.Context, p string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	u, err := b.checkPath(ctx, path.Dir(p)) // папка объекта должна быть нашим календарём
	if err != nil {
		return nil, err
	}
	name := path.Base(p) // "lesson-12.ics"
	idStr := strings.TrimSuffix(strings.TrimPrefix(name, "lesson-"), ".ics")
	id, err := strconv.ParseInt(idStr, 10, 64) // вытаскиваем номер занятия
	if err != nil {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("нет такого объекта"))
	}
	lessons, err := b.Store.LessonsForUser(u)
	if err != nil {
		return nil, err
	}
	for _, l := range lessons {
		if l.ID == id { // отдаём только события, видимые ЭТОМУ пользователю
			return b.object(u, l)
		}
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("нет такого объекта"))
}

// QueryCalendarObjects — REPORT calendar-query. Бонус новой версии библиотеки:
// готовая функция caldav.Filter сама применяет фильтр клиента (включая диапазон
// времени), а nil-запрос возвращает всё — больше не нужно отдавать всё подряд.
func (b *Backend) QueryCalendarObjects(ctx context.Context, p string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	objects, err := b.ListCalendarObjects(ctx, p, nil) // сначала все наши объекты
	if err != nil {
		return nil, err
	}
	return caldav.Filter(query, objects) // библиотека отфильтрует по запросу клиента
}

// PutCalendarObject — ИСПРАВЛЕННАЯ сигнатура возврата: (*caldav.CalendarObject, error).
// Запись запрещена: расписание меняет только сервер (CSV/заявки), календарь read-only.
func (b *Backend) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	return nil, webdav.NewHTTPError(http.StatusForbidden, errors.New("календарь только для чтения"))
}

func (b *Backend) DeleteCalendarObject(ctx context.Context, p string) error {
	return webdav.NewHTTPError(http.StatusForbidden, errors.New("календарь только для чтения"))
}
