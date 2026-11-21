const form = document.querySelector('#register-form')
const statusEl = document.querySelector('#status')
const btn = form.querySelector('.submit-button')
function setStatus(t, err = false) {
	statusEl.textContent = t
	statusEl.classList.toggle('status--err', err)
	statusEl.classList.toggle('status--ok', !err)
}
form.addEventListener('submit', async (e) => {
	e.preventDefault()
	const username = document.querySelector('#username').value.trim()
	const password = document.querySelector('#password').value
	const password2 = document.querySelector('#password2').value
	if (password !== password2) { setStatus('Пароли не совпадают', true); return }
	btn.disabled = true; setStatus('Создаём аккаунт…')
	try {
		const res = await fetch('/api/register', {
			method: 'POST', headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ username, password }),
		})
		const data = await res.json()
		if (!res.ok) throw new Error(data.error || 'Ошибка сервера')
		window.location.href = data.redirect // сразу на страницу студента
	} catch (err) { setStatus(err.message, true); btn.disabled = false }
})