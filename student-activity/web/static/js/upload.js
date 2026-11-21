// ===== Ваш оригинальный код: имя выбранного файла =====
const uploadLabel = document.querySelector('.upload-btn')
const uploadInput = document.querySelector('#file-upload-btn')
const uploadFileName = document.querySelector('.section p')
uploadInput.addEventListener('change', () => {
	// ?. и ?? — защита от «Отмены» в диалоге выбора файла
	uploadFileName.textContent = uploadInput.files[0]?.name ?? 'Файл не выбран'
})

// ===== Отправка CSV на сервер =====
const form = document.querySelector('#upload-form')
const statusEl = document.querySelector('#status')
const resultEl = document.querySelector('#result')
const btn = form.querySelector('.submit-button')
function setStatus(t, err = false) {
	statusEl.textContent = t
	statusEl.classList.toggle('status--err', err)
	statusEl.classList.toggle('status--ok', !err)
}
form.addEventListener('submit', async (e) => {
	e.preventDefault()
	if (!uploadInput.files.length) { setStatus('Выберите .csv файл', true); return }
	const fd = new FormData()
	fd.append('file', uploadInput.files[0]) // поле "file" ждёт сервер
	btn.disabled = true; setStatus('Импортируем…')
	resultEl.hidden = true
	try {
		const res = await fetch('/admin/upload', { method: 'POST', body: fd }) // boundary ставит браузер
		const data = await res.json()
		console.log(fd.entries)
		if (!res.ok) throw new Error(data.error || 'Ошибка сервера')
		setStatus(`Готово: добавлено ${data.added}, обновлено ${data.updated} (SEQUENCE+1), без изменений ${data.unchanged}, пропущено ${data.skipped}`)
		if (data.warnings && data.warnings.length) {
			resultEl.textContent = 'Предупреждения:\n' + data.warnings.join('\n')
			resultEl.hidden = false
		}
		form.reset(); uploadFileName.textContent = 'Файл не выбран'
	} catch (err) { setStatus(err.message, true) }
	finally { btn.disabled = false }
})