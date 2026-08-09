// Chrome renders an SVG favicon as a still frame: the CSS animation in
// logo.svg plays in the page and in Firefox's tab strip, but not in Chrome's.
// So the blink is driven from here instead, by swapping the icon's href through
// a handful of pre-rendered frames.
//
// The frames are built from the real logo.svg rather than from a second copy of
// the artwork: the file is fetched once, its <style> dropped, and the dot's
// group given an explicit transform per frame. Editing the logo therefore
// changes this too, and there is nothing here to keep in sync.

// scaleY and opacity per frame, closing and opening again. The rest frame is
// last, so the loop pauses on a fully open dot.
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

// The dot's centre in the coordinate system of its own group, which is where
// logo.svg's `transform-origin: center` puts it.
const ORIGIN = { x: 50, y: 39 }

async function buildFrames(src: string): Promise<string[]> {
  const res = await fetch(src)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const doc = new DOMParser().parseFromString(await res.text(), 'image/svg+xml')

  const lid = doc.querySelector('.lid')
  if (!lid) throw new Error('logo.svg has no .lid group to animate')

  // The stylesheet's own animation would fight the per-frame transform, and in
  // Firefox both would run at once.
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

  // The file as authored, restored whenever the animation is off. Read before
  // the first swap, since every later href is a data: URL.
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
    // Rests on the open frame; the closing frames run back to back.
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

  // Not paused while the tab is hidden: a background tab is exactly when the
  // favicon is the only part of this app on screen.
  reduce.addEventListener('change', () => (reduce.matches ? stop() : start()))

  // Fire and forget: a favicon is not worth delaying the app's first render,
  // and a failure here leaves the still icon the document already has.
  buildFrames(still)
    .then((built) => {
      frames = built
      if (!reduce.matches) start()
    })
    .catch(() => {})
})
