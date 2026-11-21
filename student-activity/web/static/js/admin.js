// Принять/отклонить заявку
document.querySelectorAll('.approve-btn, .reject-btn').forEach(btn => {
	btn.addEventListener('click', async () => {
		const id = btn.dataset.id
		const approve = btn.classList.contains('approve-btn')
		if (!confirm(`${approve ? 'Принять' : 'Отклонить'} заявку №${id}?` +
			(approve ? ' Аудитория будет занята, лекция добавится в расписание.' : ''))) return
		btn.disabled = true
		try {
			const res = await fetch(`/api/admin/requests/${id}/decide`, {
				method: 'POST', headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ approve }),
			})
			const data = await res.json().catch(() => ({}))
			if (!res.ok) throw new Error(data.error || 'Ошибка сервера')
			window.location.reload() // сервер перерисует таблицу с новым статусом
		} catch (err) { alert(err.message); btn.disabled = false }
	})
})
document.querySelector('#logout-btn')?.addEventListener('click', async () => {
	try { await fetch('/api/logout', { method: 'POST' }) } catch (_) {}
	window.location.href = '/login'
})