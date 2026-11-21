// ================= ВАШ ОРИГИНАЛЬНЫЙ КОД (имя файла) =================
const uploadLabel = document.querySelector('.upload-btn')     // кнопка-лейбл
const uploadInput = document.querySelector('#file-upload-btn')// скрытый input[type=file]
const uploadFileName = document.querySelector('.section p')   // абзац под кнопкой

uploadInput.addEventListener('change', () => {
	// ?. и ?? добавлены: если пользователь открыл диалог и нажал «Отмена»,
	// files пуст — оригинальный files[0].name упал бы с TypeError
	uploadFileName.textContent = uploadInput.files[0]?.name ?? 'Файл не выбран'
})

// ================= ДОБАВЛЕНО: связь с Go-сервером =================
const form = document.querySelector('#upload-form')
const statusEl = document.querySelector('#status')
const submitBtn = form.querySelector('.submit-button')

// Номер заявки приехал в адресе: /upload?id=12 → достаём "12"
const submissionId = new URLSearchParams(window.location.search).get('id')

function setStatus(text, isError = false) { // тот же помощник, что на главной
	statusEl.textContent = text
	statusEl.classList.toggle('status--err', isError)
	statusEl.classList.toggle('status--ok', !isError)
}

// Без номера крепить файл не к чему — сразу объясняем и блокируем кнопку
if (!submissionId) {
	setStatus('Сначала отправьте отчёт на главной странице', true)
	submitBtn.disabled = true
}

form.addEventListener('submit', async (event) => {
	event.preventDefault() // без перезагрузки страницы
	if (!submissionId) return // защита, хотя кнопка и заблокирована
	if (!uploadInput.files.length) { setStatus('Выберите файл', true); return }

	// FormData — формат multipart: так браузеры передают файлы
	const fd = new FormData()
	fd.append('submission_id', submissionId)  // текстовое поле — к какой заявке
	fd.append('file', uploadInput.files[0])   // сам файл (имя поля "file" ждёт сервер)

	submitBtn.disabled = true // от двойной отправки
	setStatus('Загружаем…')
	try {
		const response = await fetch('/api/upload', {
			method: 'POST',
			body: fd, // Content-Type НЕ задаём: браузер сам добавит multipart с boundary
		})
		const data = await response.json()
		if (!response.ok) throw new Error(data.error || 'Ошибка сервера')

		setStatus(`Файл «${data.name}» прикреплён к отчёту №${submissionId}. Можно загрузить ещё или вернуться к форме.`)
		form.reset() // очищаем выбор файла для возможной следующей загрузки
		uploadFileName.textContent = 'Файл не выбран'
	} catch (err) {
		setStatus(err.message, true)
	} finally {
		submitBtn.disabled = false // finally выполнится и при успехе, и при ошибке
	}
})