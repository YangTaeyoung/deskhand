// renderer runs in the page: it logs on load and wires the two buttons.
// The compute button runs a function with locals for a breakpoint; the
// second button throws an uncaught exception.

// tallyPrices sums the prices; it keeps a few locals the debugger can read.
function tallyPrices(items) {
  let total = 0
  let highest = 0
  for (const it of items) {
    total += it.price
    if (it.price > highest) highest = it.price
  }
  const note = `${items.length}개 품목`
  return { total, highest, note }
}

function onCompute() {
  const items = [
    { price: 1500 },
    { price: 2500 },
    { price: 3000 },
  ]
  const summary = tallyPrices(items)
  console.log('renderer 합계', summary.total, summary.note)
  document.getElementById('result').textContent = `결과: ${summary.total}원 (${summary.note})`
}

function onBoom() {
  console.warn('의도된 예외를 던집니다')
  const broken = null
  // Throws TypeError: Cannot read properties of null (reading 'total').
  return broken.total
}

window.addEventListener('DOMContentLoaded', () => {
  console.log('renderer 준비 완료 — 서울')
  document.getElementById('compute').addEventListener('click', onCompute)
  document.getElementById('boom').addEventListener('click', onBoom)
})
