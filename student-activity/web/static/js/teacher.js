const D = JSON.parse(document.body.dataset.app)
const $ = (s) => document.querySelector(s)
const statusEl = $('#status'), btn = $('#request-form .submit-button')
function setStatus(t, err = false) {
	statusEl.textContent = t
	statusEl.classList.toggle('status--err', err)
	statusEl.classList.toggle('status--ok', !err)
}

// Заполняем селекты данными сервера
const target = $('#target')
target.insertAdjacentHTML('beforeend',
	`<optgroup label="Группы">${D.groups.map(g => `<option value="${g}">Группа ${g}</option>`).join('')}</optgroup>`)
console.log(D)
 target.insertAdjacentHTML('beforeend',
	`<optgroup label="Доп. активности">${D.extras.map(a => `<option value="${a}">${a}</option>`).join('')}</optgroup>`)
for (const d of D.days) $('#day').insertAdjacentHTML('beforeend', `<option value="${d.n}">${d.label}</option>`)
for (const p of D.pairs) $('#pair').insertAdjacentHTML('beforeend', `<option value="${p.n}">${p.label}</option>`)
for (const w of D.weeks) $('#week').insertAdjacentHTML('beforeend', `<option value="${w.v}">${w.label}</option>`)

// Свободные аудитории: запрашиваем при каждом изменении дня/пары/недели
const room = $('#room')
async function loadRooms() {
	const day = $('#day').value, pair = $('#pair').value, week = $('#week').value
	if (!day || !pair || !week) { room.disabled = true; return } // ещё не всё выбрано
	room.disabled = true
	room.innerHTML = '<option value="">Загрузка…</option>'
	try {
		const res = await fetch(`/api/rooms/free?day=${day}&pair=${pair}&week=${week}`)
		const data = await res.json()
		if (!res.ok) throw new Error(data.error || 'Ошибка')
		if (!data.rooms || !data.rooms.length) {
			room.innerHTML = '<option value="">Свободных аудиторий нет</option>'
			return
		}
		room.innerHTML = '<option value="" disabled selected>— Выберите аудиторию —</option>' +
			data.rooms.map(r => `<option value="${r}">${r}</option>`).join('')
		room.disabled = false
	} catch (err) {
		room.innerHTML = '<option value="">Ошибка загрузки</option>'
		setStatus(err.message, true)
	}
}
for (const id of ['#day', '#pair', '#week']) $(id).addEventListener('change', loadRooms)

// Отправка заявки
$('#request-form').addEventListener('submit', async (e) => {
	e.preventDefault()
	const body = {
		title: $('#title').value.trim(), target: target.value,
		day: Number($('#day').value), pair: Number($('#pair').value),
		week: $('#week').value, place: room.value,
	}
	if (!body.title || !body.target || !body.day || !body.pair || !body.week || !body.place) {
		setStatus('Заполните все поля', true); return
	}
	btn.disabled = true; setStatus('Отправляем…')
	try {
		const res = await fetch('/api/teacher/requests', {
			method: 'POST', headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body),
		})
		const data = await res.json()
		if (!res.ok) throw new Error(data.error || 'Ошибка сервера')
		setStatus('Заявка создана! Обновляем список…')
		setTimeout(() => window.location.reload(), 700) // список заявок рендерит сервер
	} catch (err) { setStatus(err.message, true); btn.disabled = false }
})

$('#logout-btn')?.addEventListener('click', async () => {
    try { await fetch('/api/logout', { method: 'POST' }) } catch (_) {}
    window.location.href = '/login'
})