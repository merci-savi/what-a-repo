const uploadLabel = document.querySelector('.upload-btn')
const uploadInput = document.querySelector('#file-upload-btn')
const uploadFileName = document.querySelector('.section p')

uploadInput.addEventListener('change', () => {
    uploadFileName.textContent = uploadInput.files[0].name
})