const group = [111, 121, 131, 132, 141, 151, 181]
const activities = ['ЦОПП', 'Лекции студенческого клуба разработки', ]
const selectGroup = document.querySelector('#group')
const selectAct = document.querySelector('.activities')
for (i of group) {
    selectGroup.insertAdjacentHTML('beforeend', `<option value="group${i}">Группа ${i}</option>`)
}
for ([i, ii] of activities.entries()) {
    selectAct.insertAdjacentHTML('beforeend', `<label class="activity"> <input type="checkbox" name="activity" value="activity${i}"> <span>${ii}</span> </label>`)
}