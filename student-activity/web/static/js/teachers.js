const form = document.querySelector('#teacher-form')
const statusEl = document.querySelector('#status')
const btn = form.querySelector('.submit-button')
function setStatus(t, err = false) {
	statusEl.textContent = t
	statusEl.classList.toggle('status--err', err)
	statusEl.classList.toggle('status--ok', !err)
}
form.addEventListener('submit', async (e) => {
	e.preventDefault()
	const body = {
		username: document.querySelector('#username').value.trim(),
		full_name: document.querySelector('#full_name').value.trim(),
		password: document.querySelector('#password').value,
	}
	btn.disabled = true; setStatus('Создаём…')
	try {
		const res = await fetch('/api/admin/teachers', {
			method: 'POST', headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body),
		})
		const data = await res.json()
		if (!res.ok) throw new Error(data.error || 'Ошибка сервера')
		setStatus('Преподаватель создан! Обновляем список…')
		setTimeout(() => window.location.reload(), 700)
	} catch (err) { setStatus(err.message, true); btn.disabled = false }
})