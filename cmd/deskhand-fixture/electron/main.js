const { app, BrowserWindow, ipcMain } = require('electron')
const path = require('node:path')

// calcTotal sums the prices of the items; it holds a few locals for the
// debugger to read at a breakpoint.
function calcTotal(items) {
  let total = 0
  let count = 0
  for (const it of items) {
    total += it.price
    count += 1
  }
  const label = `${count}개 합계`
  return { total, count, label }
}

function createWindow() {
  const win = new BrowserWindow({
    width: 720,
    height: 520,
    title: 'Deskhand 디버그 픽스처',
    webPreferences: {
      preload: path.join(__dirname, 'renderer.js'),
      contextIsolation: false,
      nodeIntegration: true,
      sandbox: false,
    },
  })
  win.loadFile('index.html')
}

app.whenReady().then(() => {
  console.log('main 준비 완료')
  const order = { items: [{ price: 10 }, { price: 20 }, { price: 30 }], city: '서울' }
  const result = calcTotal(order.items)
  console.log('main 합계', result.total, result.label, order.city)

  ipcMain.on('compute', (event, items) => {
    const r = calcTotal(items)
    console.log('compute 요청 처리', r.total)
    event.returnValue = r.total
  })

  createWindow()
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})
