const form = document.querySelector('#login-form')       // форма входа
const statusEl = document.querySelector('#status')       // сообщения
const submitBtn = form.querySelector('.submit-button')   // кнопка

function setStatus(text, isError = false) { // помощник как на других страницах
	statusEl.textContent = text
	statusEl.classList.toggle('status--err', isError)
	statusEl.classList.toggle('status--ok', !isError)
}

form.addEventListener('submit', async (event) => {
	event.preventDefault() // отменяем перезагрузку
	const username = document.querySelector('#username').value.trim() // trim — без пробелов по краям
	const password = document.querySelector('#password').value
	if (!username || !password) { setStatus('Введите логин и пароль', true); return }

	submitBtn.disabled = true
	setStatus('Проверяем…')
	try {
		const response = await fetch('/api/login', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ username, password }), // короткая запись {username: username, ...}
		})
		const data = await response.json()
		if (!response.ok) throw new Error(data.error || 'Ошибка сервера')
		// Сервер уже поставил HttpOnly-куку с сессией — переходим в админку
		window.location.href = data.redirect || '/admin'
	} catch (err) {
		setStatus(err.message, true)
		submitBtn.disabled = false
	}
})