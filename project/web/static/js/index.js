// ================= ВАШ ОРИГИНАЛЬНЫЙ КОД (генерация списков) =================
const group = [111, 121, 131, 132, 141, 151, 181] // справочник групп
const activities = ['ЦОПП', 'Лекции студенческого клуба разработки', ] // мероприятия
const selectGroup = document.querySelector('#group')      // находим <select>
const selectAct = document.querySelector('.activities')   // контейнер чекбоксов

for (const i of group) { // const добавлен: без него i стала бы глобальной переменной
	// добавляем <option> в конец списка; value="group111" — код для сервера
	selectGroup.insertAdjacentHTML('beforeend', `<option value="group${i}">Группа ${i}</option>`)
}
for (const [i, ii] of activities.entries()) { // entries() даёт пары [индекс, значение]
	// чекбоспары: value="activity0" — индекс мероприятия, текст — название
	selectAct.insertAdjacentHTML('beforeend', `<label class="activity"> <input type="checkbox" name="activity" value="activity${i}"> <span>${ii}</span> </label>`)
}

// ================= ДОБАВЛЕНО: связь с Go-сервером =================
const form = document.querySelector('#report-form')   // наша форма
const statusEl = document.querySelector('#status')    // абзац для сообщений
const submitBtn = form.querySelector('.submit-button')// кнопка отправки

// setStatus — помощник: пишет текст и красит его в зелёный/красный
function setStatus(text, isError = false) {
	statusEl.textContent = text                              // textContent безопасен (не HTML)
	statusEl.classList.toggle('status--err', isError)        // класс ошибки — по флагу
	statusEl.classList.toggle('status--ok', !isError)        // класс успеха — иначе
}

// submit — событие отправки формы; async позволяет использовать await внутри
form.addEventListener('submit', async (event) => {
	event.preventDefault() // ОТМЕНЯЕМ стандартную перезагрузку страницы — шлём сами

	// собираем value всех ОТМЕЧЕННЫХ чекбоксов: NodeList → массив → map
	const checked = [...document.querySelectorAll('input[name="activity"]:checked')]
		.map(el => el.value)

	// клиентская проверка для удобства (сервер проверит ещё раз — ему доверяем)
	if (!selectGroup.value) { setStatus('Выберите группу', true); return }
	if (checked.length === 0) { setStatus('Отметьте хотя бы одно мероприятие', true); return }

	submitBtn.disabled = true // блокируем кнопку от двойного клика
	setStatus('Отправляем…')
	try {
		// fetch — встроенный HTTP-клиент браузера; ждём ответ через await
		const response = await fetch('/api/submissions', {
			method: 'POST',                                   // создаём ресурс
			headers: { 'Content-Type': 'application/json' },  // тело — JSON
			body: JSON.stringify({ group: selectGroup.value, activities: checked }),
		})
		const data = await response.json() // разбираем JSON-ответ сервера
		if (!response.ok) throw new Error(data.error || 'Ошибка сервера') // 4xx/5xx → в catch

		setStatus(`Отчёт №${data.id} сохранён! Открываем загрузку документа…`)
		// через секунду переходим к прикреплению файла, передавая номер заявки в URL
		setTimeout(() => { window.location.href = `/upload?id=${data.id}` }, 900)
	} catch (err) {
		setStatus(err.message, true) // показали ошибку...
		submitBtn.disabled = false   // ...и разблокировали кнопку для повторной попытки
	}
})