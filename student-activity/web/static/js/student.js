const D = JSON.parse(document.body.dataset.app)
const group = D.groups.length ? D.groups : [111, 121, 131, 132, 141, 151, 181] // запасной список
const activities = D.activities.length ? D.activities : ['ЦОПП', 'Лекции студенческого клуба разработки']
const selectGroup = document.querySelector('#group')
const selectAct = document.querySelector('.activities')
for (const i of group) {
	selectGroup.insertAdjacentHTML('beforeend', `<option value="group${i}">Группа ${i}</option>`)
}
for (const [i, ii] of activities.entries()) {
	selectAct.insertAdjacentHTML('beforeend', `<label class="activity"> <input type="checkbox" name="activity" value="activity${i}"> <span>${ii}</span> </label>`)
}
// Восстанавливаем сохранённый выбор, если он есть
if (D.savedGroup) selectGroup.value = D.savedGroup
for (const code of (D.savedActivityCodes || [])) {
	const input = selectAct.querySelector(`input[value="activity${code}"]`)
	if (input) input.checked = true
}

// ===== Ссылки календаря =====
const linksBox = document.querySelector('#links')
function showLinks(links) {
	document.querySelector('#ical-link').value = links.ical
	document.querySelector('#webcal-link').href = links.webcal
	document.querySelector('#caldav-url').textContent = links.caldav
	document.querySelector('#caldav-user').textContent = links.caldavUser
	document.querySelector('#caldav-pass').textContent = links.caldavPass
	linksBox.hidden = false // показываем блок
}
if (D.hasProfile) showLinks(D.links) // профиль уже сохранён — ссылки сразу видны

document.querySelector('#copy-btn').addEventListener('click', async () => {
	const input = document.querySelector('#ical-link')
	try { await navigator.clipboard.writeText(input.value) } // современное API буфера
	catch (_) { input.select(); document.execCommand('copy') } // запасной способ
	setStatus('Ссылка скопирована')
})

// ===== Отправка профиля =====
const form = document.querySelector('#report-form')
const statusEl = document.querySelector('#status')
const btn = form.querySelector('.submit-button')
function setStatus(t, err = false) {
	statusEl.textContent = t
	statusEl.classList.toggle('status--err', err)
	statusEl.classList.toggle('status--ok', !err)
}
form.addEventListener('submit', async (e) => {
	e.preventDefault()
	const checked = [...document.querySelectorAll('input[name="activity"]:checked')].map(el => el.value)
	if (!selectGroup.value) { setStatus('Выберите группу', true); return }
	btn.disabled = true; setStatus('Сохраняем…')
	try {
		const res = await fetch('/api/student/profile', {
			method: 'POST', headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ group: selectGroup.value, activities: checked }),
		})
		const data = await res.json()
		if (!res.ok) throw new Error(data.error || 'Ошибка сервера')
		showLinks(data.links) // сервер вернул свежие ссылки
		setStatus('Сохранено! Календарь будет обновляться автоматически.')
	} catch (err) { setStatus(err.message, true) }
	finally { btn.disabled = false }
})

// Выход (общий для всех страниц с кнопкой)
document.querySelector('#logout-btn')?.addEventListener('click', async () => {
	try { await fetch('/api/logout', { method: 'POST' }) } catch (_) {}
	window.location.href = '/login'
})