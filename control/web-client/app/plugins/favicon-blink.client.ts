
const BLINK: Array<[number, number]> = [
  [0.62, 1],
  [0.28, 0.6],
  [0.04, 0],
  [0.28, 0.6],
  [0.62, 1],
  [1, 1],
]

const FRAME_MS = 45
const OPEN_MS = 3_900

const ORIGIN = { x: 50, y: 39 }

async function buildFrames(src: string): Promise<string[]> {
  const res = await fetch(src)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const doc = new DOMParser().parseFromString(await res.text(), 'image/svg+xml')

  const lid = doc.querySelector('.lid')
  if (!lid) throw new Error('logo.svg has no .lid group to animate')

  doc.querySelector('style')?.remove()

  const serializer = new XMLSerializer()
  return BLINK.map(([scale, opacity]) => {
    lid.setAttribute(
      'transform',
      `translate(${ORIGIN.x} ${ORIGIN.y}) scale(1 ${scale}) translate(${-ORIGIN.x} ${-ORIGIN.y})`,
    )
    lid.setAttribute('opacity', String(opacity))
    return `data:image/svg+xml,${encodeURIComponent(serializer.serializeToString(doc))}`
  })
}

export default defineNuxtPlugin(() => {
  const link = document.querySelector<HTMLLinkElement>('link[rel="icon"][type="image/svg+xml"]')
  if (!link) return

  const still = link.href
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)')

  let frames: string[] = []
  let step = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  function next() {
    const frame = frames[step]
    if (!frame) return
    link.href = frame

    const last = step === frames.length - 1
    step = last ? 0 : step + 1
    timer = setTimeout(next, last ? OPEN_MS : FRAME_MS)
  }

  function start() {
    if (timer || !frames.length) return
    step = 0
    next()
  }

  function stop() {
    clearTimeout(timer)
    timer = undefined
    link.href = still
  }

  reduce.addEventListener('change', () => (reduce.matches ? stop() : start()))

  buildFrames(still)
    .then((built) => {
      frames = built
      if (!reduce.matches) start()
    })
    .catch(() => {})
})
