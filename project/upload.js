const uploadLabel = document.querySelector('.upload-btn')
const uploadInput = document.querySelector('#file-upload-btn')
const uploadFileName = document.querySelector('.section p')

uploadInput.addEventListener('change', () => {
    uploadFileName.textContent = uploadInput.files[0].name
})

// async function getData(id) {
//   const url = `http://127.0.0.1:3000/customers/${id}`;
//   try {
//     const response = await fetch(url);
//     if (!response.ok) {
//       throw new Error(`Response status: ${response.status}`);
//     }

//     const result = await response.json();
//     console.log(result);
//   } catch (error) {
//     console.error(error.message);
//   }
// }
// getData(5);


async function f() {
    const response = await fetch("http://127.0.0.1:3000/customers/", {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({ login: "example", passw: "example1233" }),
    });
}
f()