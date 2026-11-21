// Удаление заявок: вешаем обработчик на каждую кнопку «Удалить»
document.querySelectorAll('.delete-btn').forEach(btn => {
	btn.addEventListener('click', async () => {
		const id = btn.dataset.id // data-id из HTML → dataset.id
		// confirm — встроенный диалог подтверждения: удаление необратимо
		if (!confirm(`Удалить заявку №${id} вместе с файлами?`)) return
		btn.disabled = true // от двойного клика, пока идёт запрос
		try {
			const response = await fetch(`/api/submissions/${id}`, { method: 'DELETE' })
			if (!response.ok) {
				const data = await response.json().catch(() => ({})) // ответ может быть не JSON
				throw new Error(data.error || 'Не удалось удалить')
			}
			// Успех: убираем строку из таблицы без перезагрузки (оптимистичный UI)
			document.querySelector(`tr[data-id="${id}"]`)?.remove()
			const left = document.querySelectorAll('tbody tr').length // сколько осталось
			document.querySelector('#count').textContent = `Всего: ${left}` // обновляем счётчик
			if (left === 0) window.location.reload() // пусто → сервер отдаст красивую заглушку
		} catch (err) {
			alert(err.message) // в админке допустим простой alert
			btn.disabled = false
		}
	})
})

// Выход: гасим сессию на сервере и уходим на страницу входа
document.querySelector('#logout-btn').addEventListener('click', async () => {
	try { await fetch('/api/logout', { method: 'POST' }) } catch (_) { /* сеть упала — всё равно уходим */ }
	window.location.href = '/login'
})