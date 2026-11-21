const form = document.querySelector('#login-form')
const statusEl = document.querySelector('#status')
const btn = form.querySelector('.submit-button')
function setStatus(t, err = false) {
	statusEl.textContent = t
	statusEl.classList.toggle('status--err', err)
	statusEl.classList.toggle('status--ok', !err)
}
form.addEventListener('submit', async (e) => {
	e.preventDefault() // без перезагрузки
	const username = document.querySelector('#username').value.trim()
	const password = document.querySelector('#password').value
	if (!username || !password) { setStatus('Введите логин и пароль', true); return }
	btn.disabled = true; setStatus('Проверяем…')
	try {
		const res = await fetch('/api/login', {
			method: 'POST', headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ username, password }),
		})
		const data = await res.json()
		if (!res.ok) throw new Error(data.error || 'Ошибка сервера')
		window.location.href = data.redirect // сервер сказал, куда идти по роли
	} catch (err) { setStatus(err.message, true); btn.disabled = false }
})