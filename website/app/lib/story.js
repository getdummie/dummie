const NS = 'http://www.w3.org/2000/svg'
const ORB = 120
const clamp = (v, a = 0, b = 1) => Math.min(b, Math.max(a, v))
const seg = (p, a, b) => clamp((p - a) / (b - a))
const lerp = (a, b, t) => a + (b - a) * t
const easeOut = t => 1 - Math.pow(1 - t, 3)
const ease = t => t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2
const inWin = (p, a, b) => p >= a && p <= b
const pad = n => String(n).padStart(2, '0')
const hms = s => `${pad(Math.floor(s / 3600))}:${pad(Math.floor(s / 60) % 60)}:${pad(Math.floor(s) % 60)}`
const clockStr = d => `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`

function S(tag, attrs = {}, parent) {
  const e = document.createElementNS(NS, tag)
  for (const k in attrs) {
    const v = attrs[k]
    if (typeof v === 'string' && v.startsWith('var(')) e.style[k] = v
    else e.setAttribute(k, v)
  }
  if (parent) parent.appendChild(e)
  return e
}
function T(parent, x, y, s, cls, anchor = 'start') {
  const t = S('text', { x, y, class: cls, 'text-anchor': anchor }, parent)
  t.textContent = s
  return t
}
const R = (parent, x, y, w, h, cls = 'nb', rx = 8) => S('rect', { x, y, width: w, height: h, rx, class: cls }, parent)
const P = (parent, d, cls = 'wire') => S('path', { d, class: cls }, parent)
function at(path, t) {
  if (!path.__len) path.__len = path.getTotalLength()
  return path.getPointAtLength(clamp(t) * path.__len)
}
const place = (el, pt) => el.setAttribute('transform', `translate(${pt.x.toFixed(1)} ${pt.y.toFixed(1)})`)
const show = (el, o) => { el.style.opacity = o }
function glowDef(svg, id) {
  const defs = S('defs', {}, svg)
  const f = S('filter', { id, x: '-100%', y: '-100%', width: '300%', height: '300%' }, defs)
  S('feGaussianBlur', { stdDeviation: 4, result: 'b' }, f)
  const m = S('feMerge', {}, f)
  S('feMergeNode', { in: 'b' }, m)
  S('feMergeNode', { in: 'SourceGraphic' }, m)
  return `url(#${id})`
}
function keyGlyph(parent, glow, big) {
  const g = S('g', { opacity: 0, filter: glow }, parent)
  const w = big ? 2.5 : 2.2
  S('circle', { cx: big ? -6 : -5, cy: 0, r: big ? 6 : 5, fill: 'none', stroke: 'var(--amber)', 'stroke-width': w }, g)
  S('path', { d: big ? 'M0 0 H14 M10 0 V5 M14 0 V4' : 'M0 0 H11 M8 0 V4 M11 0 V3', stroke: 'var(--amber)', 'stroke-width': w, fill: 'none', 'stroke-linecap': 'round' }, g)
  return g
}
function packet(parent, color, filter, r = 6) {
  const g = S('g', { opacity: 0 }, parent)
  S('circle', { r, fill: color, filter }, g)
  return g
}
function mulberry(a) {
  return () => {
    a |= 0; a = a + 0x6D2B79F5 | 0
    let t = Math.imul(a ^ a >>> 15, 1 | a)
    t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t
    return ((t ^ t >>> 14) >>> 0) / 4294967296
  }
}
function dissolveEl(el, d) {
  if (d <= 0) { el.style.webkitMaskImage = el.style.maskImage = ''; return }
  const e = (d / 0.45) * 100
  el.style.webkitMaskImage = el.style.maskImage = `linear-gradient(90deg, transparent ${e - 10}%, #000 ${e + 10}%)`
}

export function mountStory(root) {
  const $ = s => root.querySelector(s)
  const $$ = s => [...root.querySelectorAll(s)]
  const RM = matchMedia('(prefers-reduced-motion: reduce)')
  const NARROW = matchMedia('(max-width: 760px)')
  const ac = new AbortController()
  const sig = { signal: ac.signal }
  const on = (t, ev, fn, o = {}) => t.addEventListener(ev, fn, { ...o, ...sig })
  const token = name => getComputedStyle(root).getPropertyValue(name).trim()
  let hdr = 0
  const measureHdr = () => {
    hdr = document.querySelector('header')?.offsetHeight || 0
    root.style.setProperty('--hdr', `${hdr}px`)
    root.style.setProperty('--ftr', `${document.querySelector('footer')?.offsetHeight || 0}px`)
  }
  measureHdr()

  /* particles: redrawn every frame from whatever the visible scenes register */
  const fx = { c: $('#fx'), emit: [] }
  fx.ctx = fx.c.getContext('2d')
  const sizeFx = () => {
    const dpr = Math.min(2, devicePixelRatio || 1)
    fx.c.width = innerWidth * dpr; fx.c.height = innerHeight * dpr
    fx.ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  }
  function drawFx() {
    const ctx = fx.ctx
    ctx.clearRect(0, 0, innerWidth, innerHeight)
    for (const e of fx.emit) {
      if (e.amt <= 0 || e.amt >= 1) continue
      const r = e.rect, rnd = mulberry(e.seed)
      ctx.fillStyle = token(e.color)
      for (let i = 0; i < e.n; i++) {
        const fxr = rnd(), fyr = rnd(), ang = -Math.PI / 2 + (rnd() - 0.5) * 2.4, far = 30 + rnd() * 150, sz = 1 + rnd() * 2.2
        const a = clamp((e.amt - fxr * 0.45) / 0.55)
        if (a <= 0 || a >= 1) continue
        const k = easeOut(a)
        ctx.globalAlpha = (1 - a) * 0.9
        ctx.fillRect(r.left + fxr * r.width + Math.cos(ang) * far * k + 40 * k, r.top + fyr * r.height + Math.sin(ang) * far * k, sz, sz)
      }
    }
    ctx.globalAlpha = 1
    fx.emit.length = 0
  }

  /* the protagonist */
  const orb = { el: $('#orb'), light: $('#orb-light'), halo: $('#orb-halo'), eye: $('#orb-eye'), pupil: $('#orb-pupil'), lid: $('#orb-lid'), x: 0, y: 0, s: 0.2, o: 0, g: 0.6, l: 0, lx: 0, ly: 0, init: false }
  const pointer = { x: innerWidth / 2, y: innerHeight / 2 }
  on(window, 'pointermove', e => { pointer.x = e.clientX; pointer.y = e.clientY }, { passive: true })

  function orbFrame(st, dt, now) {
    if (!st || !st.anchor) return
    const r = st.anchor.getBoundingClientRect()
    const tx = r.left + r.width / 2, ty = r.top + r.height / 2
    const ts = (st.size ?? r.width) / ORB
    const d = st.dissolve || 0
    const to = (st.opacity ?? 1) * (1 - clamp(d / 0.6))
    const snap = RM.matches || !orb.init || orb.o < 0.03
    const k = snap ? 1 : 1 - Math.exp(-dt * (st.stiff || 9))
    orb.x = lerp(orb.x, tx, k); orb.y = lerp(orb.y, ty, k); orb.s = lerp(orb.s, ts, k)
    orb.o = lerp(orb.o, to, RM.matches ? 1 : 1 - Math.exp(-dt * 8))
    orb.g = lerp(orb.g, st.glow ?? 0.6, 1 - Math.exp(-dt * 6))
    orb.l = lerp(orb.l, st.lid ?? 1, RM.matches ? 1 : 1 - Math.exp(-dt * 16))
    orb.init = true
    let lx = 0, ly = 0
    if (st.follow) {
      const dx = pointer.x - orb.x, dy = pointer.y - orb.y, m = Math.hypot(dx, dy) || 1, f = Math.min(1, m / 320)
      lx = dx / m * f; ly = dy / m * f
    } else if (st.look) { [lx, ly] = st.look }
    orb.lx = lerp(orb.lx, lx, 1 - Math.exp(-dt * 7)); orb.ly = lerp(orb.ly, ly, 1 - Math.exp(-dt * 7))
    const bt = now % 4300
    const blink = RM.matches ? 1 : bt > 4060 ? Math.abs(Math.cos((bt - 4060) / 240 * Math.PI)) : 1
    const float = st.float && !RM.matches ? Math.sin(now / 900) * 5 : 0
    orb.el.style.transform = `translate3d(${(orb.x - ORB / 2).toFixed(2)}px, ${(orb.y - ORB / 2 + float).toFixed(2)}px, 0) scale(${orb.s.toFixed(4)})`
    orb.el.style.opacity = orb.o.toFixed(3)
    orb.halo.style.opacity = (orb.g * (+token('--glow') || 1)).toFixed(3)
    orb.lid.style.transform = `scaleY(${Math.max(0.04, orb.l * blink).toFixed(3)})`
    orb.eye.setAttribute('transform', `translate(${(orb.lx * 3).toFixed(2)} ${(orb.ly * 2.5).toFixed(2)})`)
    const sq = 1 - Math.abs(orb.lx) * 0.14
    orb.pupil.setAttribute('transform', `translate(${(50 + orb.lx * 9).toFixed(2)} ${(40 + orb.ly * 8).toFixed(2)}) scale(${sq.toFixed(3)} 1) translate(-50 -40)`)
    orb.light.style.transform = `translate3d(${orb.x.toFixed(1)}px, ${orb.y.toFixed(1)}px, 0)`
    orb.light.style.opacity = (orb.o * orb.g).toFixed(3)
    if (d > 0 && d < 1) fx.emit.push({ rect: r, amt: d, color: '--oxide', n: 140, seed: 7 })
  }

  /* ---------- hero: plays once, on time rather than scroll ---------- */
  const HERO_SLOW = 2.2
  const hero = { el: $('#hero'), t0: performance.now(), t: 0, done: false, T: 2450 * HERO_SLOW }
  const H = {
    boot: $('#boot'), bootLog: $$('#boot-log div'), bootBar: $('#boot-bar'),
    term: $('#win-term'), cmd: $('#t-cmd'), ready: $('#t-ready'), local: $('#t-local'), prompt: $('#t-prompt'),
    browser: $('#win-browser'), url: $('#url'), urlTxt: $('#url-txt'), caret: $('#url-caret'), load: $('#loadbar'),
    sk: $('#sk'), real: $('#real'), badge: $('#badge'), badgeTxt: $('#badge-txt'), skip: $('#skip'),
    bootSlot: $('#boot-slot'), badgeSlot: $('#badge-slot'),
  }
  let heroLast = ''
  function heroRender(t) {
    t /= HERO_SLOW
    const q = (a, b) => seg(t, a, b)
    const bo = 1 - q(620, 860)
    H.boot.style.opacity = bo; H.boot.style.visibility = bo <= 0 ? 'hidden' : ''
    H.bootLog.forEach((l, i) => l.classList.toggle('on', t > 150 + i * 110))
    H.bootBar.style.transform = `scaleX(${q(80, 600)})`
    const te = easeOut(q(800, 1020))
    H.term.style.opacity = te; H.term.style.transform = `translateY(${(1 - te) * 14}px)`
    const cmd = '$ ' + 'bun dev'.slice(0, Math.round(q(980, 1260) * 7))
    const cmdHtml = t < 1400 && t > 800 ? cmd + '<span class="cursor"></span>' : cmd
    const key = cmdHtml + (t > 1420) + (t > 1560) + (t > 2250)
    if (key !== heroLast) {
      heroLast = key
      H.cmd.innerHTML = cmdHtml
      H.ready.textContent = t > 1420 ? 'ready in 412 ms' : ''
      H.local.textContent = t > 1560 ? '➜ local  :8000' : ''
      H.prompt.innerHTML = t > 2250 ? '$ <span class="cursor"></span>' : ''
    }
    const be = easeOut(q(1640, 1860))
    H.browser.style.opacity = be; H.browser.style.transform = `translateY(${(1 - be) * 18}px) scale(${0.97 + 0.03 * be})`
    H.urlTxt.textContent = 'my-app.dummie.app'.slice(0, Math.round(q(1700, 1980) * 17))
    H.caret.style.display = t > 1650 && t < 2000 ? '' : 'none'
    H.url.classList.toggle('secure', t > 2000)
    const lb = q(1980, 2200)
    H.load.style.transform = `scaleX(${lb})`; H.load.style.opacity = lb >= 1 ? 1 - q(2200, 2350) : 1
    const rv = q(2080, 2320)
    H.sk.style.opacity = 1 - rv; H.real.style.opacity = rv
    const up = t > 2250
    H.badge.classList.toggle('up', up)
    H.badgeTxt.textContent = up ? 'my-app · running' : 'my-app · booting'
    H.skip.style.visibility = t >= 2450 ? 'hidden' : ''
  }
  const heroSkip = () => { if (!hero.done) { hero.t = hero.T; hero.done = true; heroRender(hero.T) } }
  on(H.skip, 'click', heroSkip)
  on(window, 'keydown', e => { if (e.key === 'Escape') heroSkip() })
  on(window, 'wheel', heroSkip, { passive: true, once: true })
  on(window, 'touchmove', heroSkip, { passive: true, once: true })
  if (RM.matches) heroSkip()
  const heroScene = {
    id: 'hero', el: hero.el, tall: false,
    orb() {
      const t = hero.t / HERO_SLOW
      if (t < 640) return { anchor: H.bootSlot, lid: seg(t, 380, 540), opacity: seg(t, 0, 220), glow: 0.9, stiff: 12 }
      return { anchor: H.badgeSlot, lid: 1, glow: 0.55, stiff: 7 }
    },
  }

  /* ---------- Fast · Safe · Yours ---------- */
  const F = { svg: $('#fsy-svg'), words: $$('#fsy .w'), slot: $('#fsy-slot'), time: $('#fsy-time'), tl: $('#fsy-tl'), timer: $('#fsy-timer'), seal: $('#fsy-seal'), you: $('#fsy-you'), youT: $('#fsy-you-t') }
  {
    const s = F.svg, cx = 200, cy = 178
    s.innerHTML = ''
    const glow = glowDef(s, 'glow-fsy')
    F.ticks = []
    for (let i = 0; i < 60; i++) {
      const a = i / 60 * Math.PI * 2 - Math.PI / 2, r1 = i % 5 ? 112 : 106, r2 = 118
      F.ticks.push(S('line', { x1: cx + Math.cos(a) * r1, y1: cy + Math.sin(a) * r1, x2: cx + Math.cos(a) * r2, y2: cy + Math.sin(a) * r2, stroke: 'var(--line-2)', 'stroke-width': i % 5 ? 1 : 2 }, s))
    }
    F.ring = S('circle', { cx, cy, r: 100, fill: 'none', stroke: 'var(--oxide)', 'stroke-width': 2, pathLength: 1, 'stroke-dasharray': 1, 'stroke-dashoffset': 1, transform: `rotate(-90 ${cx} ${cy})` }, s)
    F.sealFill = S('rect', { x: 50, y: 18, width: 300, height: 300, rx: 44, fill: 'var(--oxide)', opacity: 0 }, s)
    F.sealR = S('rect', { x: 50, y: 18, width: 300, height: 300, rx: 44, fill: 'none', stroke: 'var(--oxide-t)', 'stroke-width': 2.5, pathLength: 1, 'stroke-dasharray': 1, 'stroke-dashoffset': 1 }, s)
    F.sealPulse = S('rect', { x: 50, y: 18, width: 300, height: 300, rx: 44, fill: 'none', stroke: 'var(--oxide)', 'stroke-width': 1, opacity: 0 }, s)
    F.keyPath = S('path', { d: 'M200 232 C 200 300, 200 330, 200 392', fill: 'none', stroke: 'var(--amber)', 'stroke-width': 1.5, 'stroke-dasharray': '3 5', opacity: 0 }, s)
    F.key = keyGlyph(s, glow, true)
    T(F.key, 20, 4, 'root', 'ta').setAttribute('style', 'font: 600 12px var(--fm); fill: var(--amber)')
  }
  const fsyScene = {
    id: 'fsy', el: $('#fsy'), tall: true,
    update(p) {
      const idx = p < 0.34 ? 0 : p < 0.66 ? 1 : 2
      F.words.forEach((w, i) => w.classList.toggle('on', i === idx))
      const f = seg(p, 0.04, 0.28)
      F.time.textContent = (1.84 * easeOut(f)).toFixed(2) + ' s'
      F.timer.classList.toggle('done', f >= 1)
      F.tl.textContent = f >= 1 ? 'READY' : 'BOOTING'
      F.ring.setAttribute('stroke-dashoffset', 1 - easeOut(f))
      const lit = Math.round(easeOut(f) * 60)
      F.ticks.forEach((t, i) => { t.style.stroke = i < lit ? 'var(--oxide)' : 'var(--line-2)' })
      F.sealR.setAttribute('stroke-dashoffset', 1 - ease(seg(p, 0.38, 0.58)))
      F.sealFill.setAttribute('opacity', 0.06 * seg(p, 0.56, 0.62))
      const pu = seg(p, 0.58, 0.66)
      F.sealPulse.setAttribute('opacity', pu > 0 && pu < 1 ? (1 - pu) * 0.8 : 0)
      F.sealPulse.setAttribute('transform', `translate(200 168) scale(${1 + pu * 0.12}) translate(-200 -168)`)
      F.seal.style.opacity = seg(p, 0.56, 0.6)
      const ky = seg(p, 0.7, 0.9)
      F.keyPath.setAttribute('opacity', ky > 0 ? 0.6 : 0)
      F.key.setAttribute('opacity', ky > 0 ? Math.min(1, ky * 6) : 0)
      place(F.key, at(F.keyPath, ease(ky)))
      F.you.style.opacity = seg(p, 0.66, 0.72)
      const got = ky >= 1
      F.you.classList.toggle('got', got)
      F.youT.textContent = got ? 'root@my-app:~#' : 'waiting for a key'
      F.timer.style.opacity = 1 - seg(p, 0.66, 0.7) * 0.6
    },
    orb(p) {
      const r = F.slot.getBoundingClientRect().width
      const f = easeOut(seg(p, 0.02, 0.24))
      const ky = seg(p, 0.7, 0.9)
      return { anchor: F.slot, size: r * (0.18 + 0.82 * f), opacity: 0.35 + 0.65 * f, lid: seg(p, 0.24, 0.3), glow: 0.5 + 0.4 * seg(p, 0.58, 0.64), look: ky > 0 && ky < 1 ? [0, 0.9] : [0, 0] }
    },
  }

  /* ---------- Isolation ---------- */
  const I = {
    steps: $$('#iso-steps li'), yours: $('#vm-yours'), b: $('#vm-b'), c: $('#vm-c'), slot: $('#iso-slot'),
    kvm: $('#kvm'), server: $('#server'), fin1: $('#fin1'), fin2: $('#fin2'), probe: $('#probe'), spark: $('#spark'),
    blocked: $('#blocked'), del: $('#del'), ptr: $('#ptr'), ghost: $('#ghost'), disk: $('#disk-yours'), p: 0,
  }
  const isoScene = {
    id: 'isolation', el: $('#isolation'), tall: true,
    update(p) {
      I.p = p
      const bounds = [0, 0.12, 0.24, 0.47, 0.69, 2]
      I.steps.forEach((li, i) => { li.classList.toggle('on', p >= bounds[i] && p < bounds[i + 1]); li.classList.toggle('past', p >= bounds[i + 1]) })
      const sv = easeOut(seg(p, 0, 0.1))
      I.server.style.opacity = sv; I.server.style.transform = `translateY(${(1 - sv) * 40}px)`
      const kv = easeOut(seg(p, 0.12, 0.22))
      I.kvm.style.opacity = kv; I.kvm.style.transform = `translateY(${(1 - kv) * -26}px)`
      const kf = seg(p, 0.2, 0.26)
      I.kvm.style.boxShadow = kf > 0 && kf < 1 ? `0 0 ${40 * (1 - kf)}px rgba(72,213,151,${0.6 * (1 - kf)})` : ''
      ;[I.yours, I.b, I.c].forEach((vm, i) => {
        const v = easeOut(seg(p, 0.24 + i * 0.06, 0.34 + i * 0.06))
        vm.style.opacity = v
        vm.style.transform = `translateY(${(1 - v) * -56}px)`
      })
      I.fin1.style.transform = I.fin2.style.transform = `scaleY(${ease(seg(p, 0.3, 0.46))})`

      const vb = I.b, fx1 = I.fin1.offsetLeft + 1.5
      const sx = vb.offsetLeft + vb.offsetWidth * 0.5, y = vb.offsetTop + vb.offsetHeight * 0.4
      const reach = ease(seg(p, 0.48, 0.6)), back = seg(p, 0.62, 0.68)
      const head = lerp(sx, fx1 + 3, reach) + (fx1 + 3 - sx) * -0.35 * back
      I.probe.style.opacity = p > 0.48 && p < 0.69 ? 1 - back : 0
      I.probe.style.left = `${head}px`; I.probe.style.top = `${y}px`; I.probe.style.width = `${Math.max(0, sx - head)}px`
      const hit = seg(p, 0.6, 0.67)
      I.fin1.classList.toggle('hit', hit > 0 && hit < 1)
      I.spark.style.left = `${fx1}px`; I.spark.style.top = `${y}px`
      I.spark.style.opacity = hit > 0 && hit < 1 ? 1 - hit : 0
      I.spark.style.transform = `scale(${0.3 + hit * 1.6})`
      I.blocked.style.left = `${fx1}px`
      I.blocked.style.opacity = seg(p, 0.6, 0.62) * (1 - seg(p, 0.68, 0.7))

      const yb = I.yours, cur = ease(seg(p, 0.7, 0.75))
      const dx = yb.offsetLeft + I.del.offsetLeft + I.del.offsetWidth / 2
      const dy = yb.offsetTop + I.del.offsetTop + I.del.offsetHeight / 2
      I.del.style.opacity = seg(p, 0.7, 0.72) * (1 - seg(p, 0.79, 0.81))
      I.del.style.transform = `scale(${inWin(p, 0.75, 0.77) ? 0.94 : 1})`
      I.ptr.style.opacity = seg(p, 0.7, 0.71) * (1 - seg(p, 0.79, 0.81))
      I.ptr.style.left = `${lerp(dx + 90, dx + 4, cur)}px`
      I.ptr.style.top = `${lerp(dy + 60, dy + 2, cur)}px`
      const d = seg(p, 0.78, 0.96)
      dissolveEl(I.yours, d); dissolveEl(I.disk, d)
      I.ghost.style.opacity = seg(p, 0.9, 0.97)
    },
    fx() {
      const d = seg(I.p, 0.78, 0.96)
      if (d > 0 && d < 1) {
        fx.emit.push({ rect: I.yours.getBoundingClientRect(), amt: d, color: '--oxide', n: 260, seed: 11 })
        fx.emit.push({ rect: I.disk.getBoundingClientRect(), amt: d, color: '--muted', n: 90, seed: 23 })
      }
    },
    orb(p) {
      const hit = seg(p, 0.6, 0.67)
      return { anchor: I.slot, lid: hit > 0 && hit < 1 ? 0.55 : 1, look: inWin(p, 0.5, 0.68) ? [0.9, 0] : [0, 0], dissolve: seg(p, 0.78, 0.96), glow: 0.55 }
    },
  }

  /* ---------- shared diagram scaffolding ---------- */
  function diagram(host, layouts, build) {
    const d = { host, mode: null, r: null, L: null }
    d.ensure = () => {
      const mode = NARROW.matches ? 'p' : 'l'
      if (mode === d.mode) return false
      d.mode = mode; d.L = layouts[mode]; host.innerHTML = ''
      const svg = S('svg', { viewBox: `0 0 ${d.L.w} ${d.L.h}`, class: 'dia', role: 'img', 'aria-label': host.dataset.label }, host)
      d.r = build(svg, d.L)
      return true
    }
    d.ensure()
    return d
  }

  /* ---------- Network ---------- */
  const DESTS = [
    { name: 'deb.debian.org', sub: 'debian mirror', port: 443 },
    { name: 'github.com', sub: 'git host', port: 443 },
    { name: '198.51.100.23', sub: 'unknown', port: 6667 },
  ]
  const NET_L = {
    l: { w: 720, h: 400, vm: [16, 110, 204, 180], orb: [118, 176, 34], name: [118, 244], up: [118, 264], exit: [220, 200], gate: 'v', gx: 400, gy1: 40, gy2: 360, lab: [400, 24], dests: [[610, 90], [610, 200], [610, 310]], dw: 196, dh: 50 },
    p: { w: 400, h: 440, vm: [110, 6, 180, 150], orb: [200, 58, 30], name: [200, 116], up: [200, 136], exit: [200, 156], gate: 'h', gy: 262, gx1: 14, gx2: 386, lab: [14, 250], dests: [[70, 392], [200, 392], [330, 392]], dw: 122, dh: 50 },
  }
  const netDia = diagram($('#net-dia'), NET_L, (svg, L) => {
    const r = { p1: [], p2: [], dest: [], dsub: [], teeth: [] }
    const glow = glowDef(svg, 'glow-net')
    R(svg, ...L.vm, 'nb ok', 12)
    r.orb = S('circle', { cx: L.orb[0], cy: L.orb[1], r: L.orb[2], fill: 'transparent' }, svg)
    T(svg, L.name[0], L.name[1], 'my-app', 't', 'middle')
    T(svg, L.up[0], L.up[1], 'running', 'ts tg', 'middle')
    L.dests.forEach(([x, y]) => {
      const vert = L.gate === 'v'
      const d1 = vert ? `M${L.exit[0]} ${L.exit[1]} C 320 ${L.exit[1]}, 300 ${y}, ${L.gx} ${y}` : `M${L.exit[0]} ${L.exit[1]} C ${L.exit[0]} 215, ${x} 205, ${x} ${L.gy}`
      const d2 = vert ? `M${L.gx} ${y} L ${x - L.dw / 2} ${y}` : `M${x} ${L.gy} L ${x} ${y - L.dh / 2}`
      r.p1.push(P(svg, d1, 'wire')); r.p2.push(P(svg, d2, 'wire dash'))
    })
    const gl = L.gate === 'v' ? { x1: L.gx, y1: L.gy1, x2: L.gx, y2: L.gy2 } : { x1: L.gx1, y1: L.gy, x2: L.gx2, y2: L.gy }
    S('line', { ...gl, stroke: 'var(--oxide-t)', 'stroke-width': 3, 'stroke-linecap': 'round' }, svg)
    T(svg, L.lab[0], L.lab[1], 'POLICY GATE', 'tl tg', L.gate === 'v' ? 'middle' : 'start')
    L.dests.forEach(([x, y], i) => {
      const tooth = L.gate === 'v' ? R(svg, L.gx - 7, y - 14, 14, 28, 'nb', 3) : R(svg, x - 14, L.gy - 7, 28, 14, 'nb', 3)
      tooth.style.fill = 'var(--bg)'; r.teeth.push(tooth)
      r.dest.push(R(svg, x - L.dw / 2, y - L.dh / 2, L.dw, L.dh, 'nb', 8))
      T(svg, x, y - 3, DESTS[i].name, L.gate === 'v' ? 't' : 'ts', 'middle').style.fill = 'var(--text)'
      r.dsub.push(T(svg, x, y + 14, DESTS[i].sub, 'ts', 'middle'))
    })
    r.pk = packet(svg, 'var(--oxide)', glow, 6)
    r.burst = S('circle', { r: 10, fill: 'none', stroke: 'var(--danger)', 'stroke-width': 2, opacity: 0 }, svg)
    r.liveLayer = S('g', {}, svg)
    r.glow = glow
    return r
  })
  const NET = {
    cap: $('#net-cap'), log: $('#net-log'), hint: $('#net-hint'), upt: $('#uptime'),
    sw: [$('#sw-deb'), $('#sw-gh')], rv: [$('#rv-deb'), $('#rv-gh')], rows: [$('#rule-deb'), $('#rule-gh')],
    user: [null, null], live: [], liveLog: [], lastLog: '', p: 0, lastRes: [null, null, null], gh: undefined,
  }
  const NET_SEQ = [
    { d: 0, a: 0.06, g: 0.14, b: 0.22, fate: 'allow' },
    { d: 1, a: 0.26, g: 0.34, b: 0.42, fate: 'reject' },
    { d: 2, a: 0.44, g: 0.52, b: 0.56, fate: 'drop' },
    { d: 1, a: 0.70, g: 0.78, b: 0.86, fate: 'allow' },
  ]
  const NET_LOG = [
    { p: 0.22, c: 'ok', v: 'ALLOW', s: 'deb.debian.org:443', t: '09:24:11' },
    { p: 0.35, c: 'no', v: 'REJECT', s: 'github.com:443', t: '09:24:13' },
    { p: 0.53, c: 'drop', v: 'DROP', s: '198.51.100.23:6667', t: '09:24:16' },
    { p: 0.62, c: 'rl', v: 'RULE', s: 'github.com → allow', t: '09:24:20' },
    { p: 0.86, c: 'ok', v: 'ALLOW', s: 'github.com:443', t: '09:24:22' },
  ]
  const scriptRule = (i, p) => i === 0 ? true : p >= 0.62
  const ruleOn = (i, p) => NET.user[i] ?? scriptRule(i, p)
  const FATE = { allow: ['ok', 'ALLOW', 'allowed'], reject: ['no', 'REJECT', 'refused'], drop: ['drop', 'DROP', 'dropped'] }
  function netCaption(p) {
    if (p < 0.05) return 'A fresh machine. It reaches nothing until the policy says so.'
    if (p < 0.24) return 'deb.debian.org is on the allow list. The packet passes.'
    if (p < 0.43) return 'github.com is blocked. The connection is refused.'
    if (p < 0.58) return 'Anything unlisted is dropped without an answer.'
    if (p < 0.69) return 'Allow github.com. The machine keeps running.'
    if (p < 0.9) return 'The next attempt goes through.'
    return 'Your turn. Flip a rule and send a packet.'
  }
  function renderLog(p) {
    const rows = NET_LOG.filter(l => p >= l.p).concat(NET.liveLog)
    const key = `${rows.length}:${NET.liveLog.length}`
    if (key === NET.lastLog) return
    NET.lastLog = key
    NET.log.innerHTML = ''
    rows.slice(-6).forEach(l => {
      const li = document.createElement('li')
      li.className = l.c
      li.innerHTML = `<time>${l.t}</time><b>${l.v}</b><span></span>`
      li.lastChild.textContent = l.s
      NET.log.appendChild(li)
    })
  }
  const netScene = {
    id: 'network', el: $('#network'), tall: true,
    update(p) {
      NET.p = p
      const r = netDia.r
      NET.cap.textContent = netCaption(p)
      NET.hint.classList.toggle('on', p > 0.9)
      const gh = scriptRule(1, p)
      if (NET.gh !== undefined && NET.gh !== gh && NET.user[1] === null) {
        NET.rows[1].classList.remove('flash'); void NET.rows[1].offsetWidth; NET.rows[1].classList.add('flash')
      }
      NET.gh = gh
      ;[0, 1].forEach(i => {
        const ok = ruleOn(i, p)
        NET.sw[i].setAttribute('aria-checked', ok)
        NET.rv[i].textContent = ok ? 'allow' : 'block'
        NET.rv[i].className = 'rv ' + (ok ? 'a' : 'b')
      })
      r.p2.forEach((w, i) => w.setAttribute('class', i < 2 && ruleOn(i, p) ? 'wire on' : 'wire dash'))
      renderLog(p)
      const s = NET_SEQ.find(q => p >= q.a && p <= q.b + 0.02)
      r.teeth.forEach(t => t.setAttribute('class', 'nb'))
      r.dest.forEach(t => t.setAttribute('class', 'nb'))
      show(r.burst, 0)
      if (s) {
        let pt, o = 1, col = 'var(--oxide)'
        if (p < s.g) pt = at(r.p1[s.d], ease(seg(p, s.a, s.g)))
        else {
          const t = seg(p, s.g, s.b), gatePt = at(r.p1[s.d], 1)
          if (s.fate === 'allow') { pt = at(r.p2[s.d], ease(t)); r.teeth[s.d].setAttribute('class', 'nb ok') }
          else {
            pt = s.fate === 'reject' ? at(r.p1[s.d], 1 - ease(t)) : gatePt
            if (s.fate === 'drop') o = 1 - t
            col = 'var(--danger)'
            r.teeth[s.d].setAttribute('class', 'nb bad')
            place(r.burst, gatePt); show(r.burst, t < 1 ? 1 - t : 0)
            r.burst.setAttribute('r', 6 + t * 18)
          }
          if (s.fate === 'allow' && t >= 1) r.dest[s.d].setAttribute('class', 'nb hit')
        }
        if (p > s.b) o = s.fate === 'allow' ? 1 - seg(p, s.b, s.b + 0.02) : 0
        r.pk.firstChild.style.fill = col
        place(r.pk, pt); show(r.pk, o)
      } else show(r.pk, 0)
      const res = [p >= 0.22 ? 'allowed' : null, p >= 0.86 ? 'allowed' : p >= 0.35 ? 'refused' : null, p >= 0.53 ? 'dropped' : null]
      r.dsub.forEach((t, i) => {
        const v = NET.lastRes[i] || res[i]
        t.textContent = v || DESTS[i].sub
        t.setAttribute('class', 'ts ' + (v === 'allowed' ? 'tg' : v ? 'tr' : ''))
      })
    },
    frame(now) {
      const r = netDia.r
      NET.upt.textContent = 'up ' + hms(847 + (now - boot0) / 1000)
      for (const k of NET.live) {
        const t = (now - k.t0) / 1100
        if (!k.el) k.el = packet(r.liveLayer, k.fate === 'allow' ? 'var(--oxide)' : 'var(--danger)', r.glow, 5)
        let pt, o = 1
        if (t < 0.5) pt = at(r.p1[k.d], ease(t / 0.5))
        else {
          const u = clamp((t - 0.5) / 0.5)
          if (!k.logged) {
            k.logged = true
            const [c, v, res] = FATE[k.fate]
            NET.liveLog.push({ c, v, s: `${DESTS[k.d].name}:${DESTS[k.d].port}`, t: clockStr(new Date()) })
            NET.lastRes[k.d] = res
            netScene.update(NET.p)
          }
          if (k.fate === 'allow') pt = at(r.p2[k.d], ease(u))
          else if (k.fate === 'reject') pt = at(r.p1[k.d], 1 - ease(u))
          else { pt = at(r.p1[k.d], 1); o = 1 - u }
          if (u >= 1) k.dead = true
        }
        place(k.el, pt); show(k.el, o)
      }
      NET.live = NET.live.filter(k => { if (k.dead) k.el.remove(); return !k.dead })
    },
    orb(p) {
      const w = netDia.r.orb.getBoundingClientRect().width
      const b = easeOut(seg(p, 0, 0.05))
      const s = NET_SEQ.find(q => p >= q.a && p <= q.b)
      return { anchor: netDia.r.orb, size: w * (0.3 + 0.7 * b), lid: 0.2 + 0.8 * b, look: s ? (NARROW.matches ? [0, 0.9] : [0.9, 0]) : [0, 0], glow: 0.6 }
    },
  }
  NET.sw.forEach((b, i) => on(b, 'click', () => {
    const next = !ruleOn(i, NET.p)
    NET.user[i] = next
    NET.liveLog.push({ c: 'rl', v: 'RULE', s: `${DESTS[i].name} → ${next ? 'allow' : 'block'}`, t: clockStr(new Date()) })
    netScene.update(NET.p)
  }))
  $$('.send').forEach(b => on(b, 'click', () => {
    const d = +b.dataset.d
    NET.live.push({ d, fate: d === 2 ? 'drop' : ruleOn(d, NET.p) ? 'allow' : 'reject', t0: performance.now() })
  }))

  /* ---------- Keys: github clone first, then the same path to ChatGPT and Claude ---------- */
  const KEY_CMD = ['$ git clone \\', '    https://github.int.dummie.dev/', '    acme/private.git']
  const KEY_OUT = ["Cloning into 'private'...", 'Receiving objects: 100% (1284/1284)', 'done.']
  const KEY_AI = [{ t: 'ChatGPT', s: 'api.openai.com' }, { t: 'Claude', s: 'api.anthropic.com' }]
  const GH_END = 0.8
  const KEYS_L = {
    l: { w: 960, h: 400, vm: [16, 30, 336, 340], orb: [48, 64, 16], head: [74, 69], term: [38, 116, 22],
         kf: [34, 290, 300, 62], kfL: [50, 314], kfV: [318, 336], kfS: [50, 340],
         bnd: [386, 16, 386, 384], bndL: [386, 12, 'middle'], host: [408, 30, 254, 340], hostL: [424, 54],
         ip: [428, 108, 214, 84], ipT: [446, 160], ipS: [446, 180], dc: [428, 274, 214, 72], dcT: [446, 304], dcS: [446, 328],
         gh: [764, 92, 180, 72], ghT: [854, 124], ghS: [854, 146],
         ai: [[764, 196, 180, 64], [764, 284, 180, 64]], aiT: [[854, 224], [854, 312]], aiS: [[854, 244], [854, 332]],
         req1: 'M330 128 L428 128', req2: 'M642 128 L764 128', keyP: 'M610 274 L610 136',
         aiP: ['M642 150 C 704 150, 700 228, 764 228', 'M642 172 C 704 172, 700 316, 764 316'],
         src: [535, 96, 'middle'], ok: [854, 82], tagDy: -14 },
    p: { w: 400, h: 660, vm: [10, 8, 380, 262], orb: [40, 38, 15], head: [64, 43], term: [26, 80, 19],
         kf: [24, 196, 352, 60], kfL: [38, 218], kfV: [362, 240], kfS: [38, 244],
         bnd: [8, 290, 392, 290], bndL: [392, 284, 'end'], host: [10, 304, 380, 120], hostL: [22, 322],
         ip: [22, 334, 172, 66], ipT: [36, 360], ipS: [36, 384], dc: [206, 334, 172, 66], dcT: [220, 360], dcS: [220, 384],
         gh: [8, 560, 120, 70], ghT: [68, 590], ghS: [68, 610],
         ai: [[140, 560, 120, 70], [272, 560, 120, 70]], aiT: [[200, 590], [332, 590]], aiS: [[200, 610], [332, 610]],
         req1: 'M176 262 L176 334', req2: 'M176 400 C 176 480, 68 480, 68 560', keyP: 'M290 367 L188 367',
         aiP: ['M176 400 C 176 480, 200 480, 200 560', 'M176 400 C 176 480, 332 480, 332 560'],
         src: [206, 418, 'start'], ok: [68, 650], tagDy: -14 },
  }
  const keysDia = diagram($('#keys-dia'), KEYS_L, (svg, L) => {
    const r = {}
    const glow = glowDef(svg, 'glow-keys')
    R(svg, ...L.vm, 'nb ok', 12)
    r.orb = S('circle', { cx: L.orb[0], cy: L.orb[1], r: L.orb[2], fill: 'transparent' }, svg)
    T(svg, L.head[0], L.head[1], 'vm-7f3a · guest', 'ts')
    r.lines = []
    for (let i = 0; i < 6; i++) {
      const t = T(svg, L.term[0], L.term[1] + i * L.term[2], '', 'ts')
      t.style.fill = i < 3 ? 'var(--text)' : i === 5 ? 'var(--oxide-t)' : 'var(--muted)'
      r.lines.push(t)
    }
    R(svg, ...L.kf, 'nb', 6).style.fill = 'var(--bg-2)'
    T(svg, L.kfL[0], L.kfL[1], 'CREDENTIALS IN THIS VM', 'tl')
    const v = T(svg, L.kfV[0], L.kfV[1], '0', 't tg', 'end'); v.style.fontSize = '26px'; v.style.fontWeight = '600'
    T(svg, L.kfS[0], L.kfS[1], '~/.ssh empty · no API keys', 'ts')
    r.scan = S('rect', { x: L.kf[0], y: L.kf[1], width: 2, height: L.kf[3], fill: 'var(--oxide)', opacity: 0.5 }, svg)
    S('line', { x1: L.bnd[0], y1: L.bnd[1], x2: L.bnd[2], y2: L.bnd[3], stroke: 'var(--oxide-t)', 'stroke-width': 1.5, 'stroke-dasharray': '6 6', opacity: 0.7 }, svg)
    T(svg, L.bndL[0], L.bndL[1], 'VM BOUNDARY', 'tl tg', L.bndL[2])
    R(svg, ...L.host, 'region', 12)
    T(svg, L.hostL[0], L.hostL[1], 'QEMU HOST', 'tl')
    r.req1 = P(svg, L.req1); r.req2 = P(svg, L.req2); r.keyP = P(svg, L.keyP, 'wire dash')
    r.aiP = L.aiP.map(d => P(svg, d, 'wire dash'))
    r.ip = R(svg, ...L.ip); T(svg, L.ipT[0], L.ipT[1], 'intproxy', 't'); T(svg, L.ipS[0], L.ipS[1], '10.64.255.254:443', 'ts')
    r.dc = R(svg, ...L.dc); T(svg, L.dcT[0], L.dcT[1], 'dclient', 't'); T(svg, L.dcS[0], L.dcS[1], 'brokers the tokens', 'ts')
    r.gh = R(svg, ...L.gh); T(svg, L.ghT[0], L.ghT[1], 'github.com', 't', 'middle'); T(svg, L.ghS[0], L.ghS[1], 'acme/private', 'ts', 'middle')
    r.ai = KEY_AI.map((a, i) => {
      const box = R(svg, ...L.ai[i])
      T(svg, L.aiT[i][0], L.aiT[i][1], a.t, 't', 'middle'); T(svg, L.aiS[i][0], L.aiS[i][1], a.s, 'ts', 'middle')
      return box
    })
    r.src = T(svg, L.src[0], L.src[1], 'src 10.64.0.12 → vm-7f3a', 'ts tg', L.src[2])
    r.ok = T(svg, L.ok[0], L.ok[1], '200 OK', 'ts tg', 'middle')
    r.pk = packet(svg, 'var(--oxide)', glow, 6)
    r.ring = S('circle', { r: 11, fill: 'none', stroke: 'var(--amber)', 'stroke-width': 2, opacity: 0 }, svg)
    r.tag = T(svg, 0, 0, '', 'ts', 'middle')
    r.key = keyGlyph(svg, glow)
    r.aiPk = KEY_AI.map(() => ({ pk: packet(svg, 'var(--oxide)', glow, 5), ring: S('circle', { r: 9, fill: 'none', stroke: 'var(--amber)', 'stroke-width': 2, opacity: 0 }, svg) }))
    return r
  })
  function keysCaption(g, p) {
    if (p >= GH_END) return 'ChatGPT and Claude work the same way. Their API keys stay on the host too.'
    if (g < 0.14) return 'The guest runs git clone against github.int. It has no credential to send.'
    if (g < 0.36) return 'intproxy knows the caller by its source address. nftables pins that address to one VM.'
    if (g < 0.48) return 'dclient hands over a short-lived GitHub App token, scoped to this VM and this repo.'
    if (g < 0.7) return 'The token is added outside the VM, on the way to github.com.'
    return 'The repo arrives. The VM never saw a key.'
  }
  const keysScene = {
    id: 'keys', el: $('#keys'), tall: true, cap: $('#keys-cap'),
    update(p) {
      const r = keysDia.r, L = keysDia.L
      const g = p / GH_END
      this.cap.textContent = keysCaption(g, p)
      let n = Math.round(seg(g, 0.03, 0.13) * KEY_CMD.join('').length)
      KEY_CMD.forEach((s, i) => { const k = Math.min(n, s.length); r.lines[i].textContent = s.slice(0, k); n -= k })
      KEY_OUT.forEach((s, i) => { r.lines[3 + i].textContent = g > 0.86 + i * 0.03 ? s : '' })
      const aiBusy = inWin(p, GH_END, 0.98)
      r.ip.setAttribute('class', inWin(g, 0.27, 0.66) || aiBusy ? 'nb hit' : 'nb')
      r.dc.setAttribute('class', inWin(g, 0.36, 0.48) || aiBusy ? 'nb key' : 'nb')
      r.gh.setAttribute('class', inWin(g, 0.64, 0.72) ? 'nb hit' : 'nb')
      r.req1.setAttribute('class', g > 0.14 ? 'wire on' : 'wire')
      r.req2.setAttribute('class', g > 0.48 ? 'wire on' : 'wire')
      show(r.src, seg(g, 0.28, 0.32) * (1 - seg(g, 0.5, 0.54)))
      show(r.ok, seg(g, 0.64, 0.67) * (1 - seg(g, 0.8, 0.84)))
      let pt = null, tag = '', tagCls = 'ts', carry = false
      if (inWin(g, 0.14, 0.28)) { pt = at(r.req1, ease(seg(g, 0.14, 0.28))); tag = 'no credential' }
      else if (inWin(g, 0.28, 0.48)) { const a = at(r.req1, 1), b = at(r.req2, 0), u = ease(seg(g, 0.28, 0.48)); pt = { x: lerp(a.x, b.x, u), y: lerp(a.y, b.y, u) } }
      else if (inWin(g, 0.48, 0.64)) { pt = at(r.req2, ease(seg(g, 0.48, 0.64))); carry = true; tag = '+ token (vm, repo)'; tagCls = 'ts ta' }
      else if (inWin(g, 0.64, 0.70)) { pt = at(r.req2, 1); carry = true }
      else if (inWin(g, 0.70, 0.78)) { pt = at(r.req2, 1 - ease(seg(g, 0.70, 0.78))); tag = 'objects' }
      else if (inWin(g, 0.78, 0.86)) { pt = at(r.req1, 1 - ease(seg(g, 0.78, 0.86))); tag = 'objects, no token' }
      if (pt) { place(r.pk, pt); show(r.pk, 1); r.tag.setAttribute('x', pt.x); r.tag.setAttribute('y', pt.y + L.tagDy) } else show(r.pk, 0)
      r.tag.textContent = tag; r.tag.setAttribute('class', tagCls)
      show(r.ring, carry ? 1 : 0); if (pt) place(r.ring, pt)
      const kt = seg(g, 0.36, 0.48)
      let kp = null, ko = 0
      if (kt > 0 && g < 0.48) { kp = at(r.keyP, ease(kt)); ko = 1 }
      else if (carry) { kp = { x: pt.x + 14, y: pt.y - 12 }; ko = 1 }
      else if (inWin(g, 0.70, 0.8)) { const ip = at(r.req2, 0); kp = { x: ip.x - 14, y: ip.y + 26 }; ko = 1 - seg(g, 0.72, 0.8) }
      if (kp) place(r.key, kp)
      show(r.key, ko)

      r.aiPk.forEach((a, i) => {
        const s0 = GH_END + 0.01 + i * 0.05
        let q = null, carrying = false
        if (inWin(p, s0, s0 + 0.05)) q = at(r.req1, ease(seg(p, s0, s0 + 0.05)))
        else if (inWin(p, s0 + 0.05, s0 + 0.11)) { q = at(r.aiP[i], ease(seg(p, s0 + 0.05, s0 + 0.11))); carrying = true }
        if (q) { place(a.pk, q); place(a.ring, q) }
        show(a.pk, q ? 1 : 0); show(a.ring, carrying ? 1 : 0)
        r.aiP[i].setAttribute('class', p > s0 + 0.05 ? 'wire on' : 'wire dash')
        r.ai[i].setAttribute('class', p > s0 + 0.11 ? 'nb hit' : 'nb')
      })
    },
    frame(now) {
      const r = keysDia.r, L = keysDia.L
      if (!RM.matches) r.scan.setAttribute('x', L.kf[0] + ((now / 2600) % 1) * (L.kf[2] - 2))
    },
    orb(p) {
      const w = keysDia.r.orb.getBoundingClientRect().width
      return { anchor: keysDia.r.orb, size: w * 1.1, look: inWin(p / GH_END, 0.14, 0.86) || p > GH_END ? (NARROW.matches ? [0, 0.9] : [0.9, 0]) : [0, 0], glow: 0.5 }
    },
  }

  /* ---------- Upgrades ---------- */
  const UP_L = {
    l: { w: 960, h: 360, you: [16, 90, 262, 180], yh: [34, 116], yl: [34, 146, 22],
         px: [398, 20, 194, 74], pxT: [416, 52], pxS: [416, 76], pp: [398, 238, 194, 74], ppT: [416, 270], ppS: [416, 294],
         vm: [718, 136, 226, 96], orb: [768, 184, 26], vmT: [806, 180], vmS: [806, 202],
         accept: 'M278 140 C 340 140, 340 57, 398 57', sock: 'M495 94 L495 238', sockL: [505, 170, 'start'],
         sess: 'M278 222 C 340 222, 340 275, 398 275 L592 275 C 654 275, 656 184, 718 184',
         dep: [495, 12, 'middle'], newDx: 90, newDy: 0 },
    p: { w: 400, h: 620, you: [10, 6, 380, 172], yh: [26, 32], yl: [26, 62, 21],
         px: [10, 262, 176, 74], pxT: [26, 292], pxS: [26, 316], pp: [214, 262, 176, 74], ppT: [230, 292], ppS: [230, 316],
         vm: [100, 506, 200, 96], orb: [140, 554, 24], vmT: [176, 550], vmS: [176, 572],
         accept: 'M98 178 L98 262', sock: 'M186 300 L214 300', sockL: [200, 358, 'middle'],
         sess: 'M302 178 L302 336 C 302 420, 200 430, 200 506',
         dep: [200, 226, 'middle'], newDx: 0, newDy: 60 },
  }
  const LOGS = ['GET / 200 12ms', 'GET /api/health 200 3ms', 'POST /api/items 201 18ms', 'GET /assets/app.js 200 4ms', 'GET /api/items 200 9ms', 'PUT /api/items/42 200 14ms']
  const upDia = diagram($('#up-dia'), UP_L, (svg, L) => {
    const r = {}
    const glow = glowDef(svg, 'glow-up')
    r.sess = P(svg, L.sess, 'wire'); r.sess.style.strokeWidth = 3; r.sess.setAttribute('stroke-linecap', 'round')
    r.sess.setAttribute('pathLength', 1); r.sess.style.strokeDasharray = 1
    r.accept = P(svg, L.accept, 'wire'); r.accept.setAttribute('pathLength', 1); r.accept.style.strokeDasharray = 1
    r.sock = P(svg, L.sock, 'wire dash')
    r.sockL = T(svg, L.sockL[0], L.sockL[1], 'unix socket', 'ts', L.sockL[2])
    R(svg, ...L.you, 'nb ok', 12)
    T(svg, L.yh[0], L.yh[1], 'YOU · SSH root@my-app', 'tl tg')
    r.yl = []
    for (let i = 0; i < 5; i++) r.yl.push(T(svg, L.yl[0], L.yl[1] + i * L.yl[2], '', 'ts'))
    const mk = (ver, isNew) => {
      const g = S('g', {}, svg)
      R(g, ...L.px)
      T(g, L.pxT[0], L.pxT[1], 'dproxy', 't')
      T(g, L.pxS[0], L.pxS[1], ver, isNew ? 'ts tg' : 'ts')
      return g
    }
    r.old = mk('v1 · accepts, routes', false)
    r.nw = mk('v2 · new build', true)
    r.pp = R(svg, ...L.pp)
    T(svg, L.ppT[0], L.ppT[1], 'dpipe', 't'); T(svg, L.ppS[0], L.ppS[1], 'holds your connection', 'ts')
    R(svg, ...L.vm, 'nb ok', 12)
    r.orb = S('circle', { cx: L.orb[0], cy: L.orb[1], r: L.orb[2], fill: 'transparent' }, svg)
    T(svg, L.vmT[0], L.vmT[1], 'my-app', 't'); T(svg, L.vmS[0], L.vmS[1], 'vm-7f3a', 'ts')
    r.dep = T(svg, L.dep[0], L.dep[1], 'DEPLOYING dproxy v2', 'tl ta', L.dep[2])
    r.port = T(svg, 0, 0, ':22', 'ts tg', 'middle')
    r.fd = packet(svg, 'var(--amber)', glow, 5)
    r.dots = []
    for (let i = 0; i < 6; i++) r.dots.push(packet(svg, 'var(--oxide)', glow, 3.5))
    return r
  })
  function upCaption(p) {
    if (p < 0.14) return 'You connect over SSH. dproxy accepts on port 22.'
    if (p < 0.26) return 'dproxy passes the file descriptor to dpipe over a unix socket.'
    if (p < 0.38) return 'dpipe now holds your session. dproxy steps out of the data path.'
    if (p < 0.66) return 'A new dproxy ships. The old one exits.'
    return 'Your session never noticed.'
  }
  const UP = { cap: $('#up-cap'), timer: $('#up-timer'), ver: $('#up-ver'), t0: null, p: 0, lines: ['root@my-app:~# tail -f app.log'], lastLine: 0, li: 0 }
  const upScene = {
    id: 'upgrades', el: $('#upgrades'), tall: true,
    update(p) {
      UP.p = p
      const r = upDia.r, L = upDia.L
      UP.cap.textContent = upCaption(p)
      r.accept.style.strokeDashoffset = 1 - ease(seg(p, 0.04, 0.13))
      r.accept.setAttribute('class', 'wire on')
      show(r.accept, 1 - seg(p, 0.3, 0.36))
      const ap = at(r.accept, 1)
      r.port.setAttribute('x', ap.x - 16); r.port.setAttribute('y', ap.y - 8)
      show(r.port, seg(p, 0.1, 0.13) * (1 - seg(p, 0.3, 0.36)))
      const fdT = seg(p, 0.15, 0.24)
      if (fdT > 0 && fdT < 1) { place(r.fd, at(r.sock, ease(fdT))); show(r.fd, 1) } else show(r.fd, 0)
      const ss = ease(seg(p, 0.22, 0.32))
      r.sess.style.strokeDashoffset = 1 - ss
      r.sess.setAttribute('class', ss > 0 ? 'wire on' : 'wire')
      r.sess.style.strokeOpacity = ss > 0 ? 0.9 : ''
      r.pp.setAttribute('class', ss >= 1 ? 'nb hit' : 'nb')
      const out = seg(p, 0.38, 0.5)
      r.old.style.opacity = 1 - out
      r.old.setAttribute('transform', `translate(0 ${-18 * out})`)
      const inn = easeOut(seg(p, 0.5, 0.64))
      r.nw.style.opacity = inn
      r.nw.setAttribute('transform', `translate(${L.newDx * (1 - inn)} ${L.newDy * (1 - inn)})`)
      const sockOn = p < 0.38 || p > 0.66
      r.sock.style.opacity = sockOn ? 1 : 0.15
      r.sockL.style.opacity = sockOn ? 1 : 0.3
      r.sock.setAttribute('class', p > 0.66 ? 'wire on dash' : 'wire dash')
      show(r.dep, inWin(p, 0.36, 0.66) ? 1 : 0)
      UP.ver.textContent = p < 0.5 ? 'v1' : p < 0.64 ? 'v1 → v2' : 'v2'
    },
    frame(now) {
      const r = upDia.r
      if (UP.t0 === null) UP.t0 = now - 192000
      UP.timer.textContent = hms((now - UP.t0) / 1000)
      const ss = seg(UP.p, 0.22, 0.32)
      r.dots.forEach((d, i) => {
        if (ss < 1 || RM.matches) { show(d, 0); return }
        const f = ((now / 2200) + i / 6) % 1
        place(d, at(r.sess, i % 2 ? 1 - f : f)); show(d, 0.9)
      })
      if (now - UP.lastLine > 900) {
        UP.lastLine = now
        if (UP.li > 0) UP.lines.push(`${clockStr(new Date())} ${LOGS[UP.li % LOGS.length]}`)
        UP.li++
        if (UP.lines.length > 5) UP.lines.splice(1, 1)
        r.yl.forEach((t, i) => { t.textContent = UP.lines[i] || ''; t.style.fill = i === 0 ? 'var(--text)' : 'var(--muted)' })
      }
    },
    orb() {
      const w = upDia.r.orb.getBoundingClientRect().width
      return { anchor: upDia.r.orb, size: w * 1.1, lid: 1, glow: 0.55, look: NARROW.matches ? [0, -0.8] : [-0.9, 0] }
    },
  }

  /* ---------- Use cases: motion only on hover, focus or tap ---------- */
  const UC = { tiles: $$('.uc'), slot: $('#uc-slot'), active: null }
  const cpu = $('#uc-cpu'), mem = $('#uc-mem'), cpuO = $('#uc-cpu-o'), memO = $('#uc-mem-o'), sum = $('#uc-sum')
  let sizeTouched = false, sizeAnim = 0
  const paintSize = () => {
    cpuO.textContent = cpu.value; memO.textContent = mem.value + ' GB'
    sum.innerHTML = `it's a computer · <b>${cpu.value} vCPU / ${mem.value} GB</b>`
  }
  ;[cpu, mem].forEach(i => on(i, 'input', () => { sizeTouched = true; cancelAnimationFrame(sizeAnim); paintSize() }))
  function animSize(toC, toM) {
    if (sizeTouched) return
    cancelAnimationFrame(sizeAnim)
    const c0 = +cpu.value, m0 = +mem.value, t0 = performance.now()
    const step = now => {
      const t = RM.matches ? 1 : ease(clamp((now - t0) / 900))
      cpu.value = Math.round(lerp(c0, toC, t)); mem.value = Math.round(lerp(m0, toM, t)); paintSize()
      if (t < 1) sizeAnim = requestAnimationFrame(step)
    }
    sizeAnim = requestAnimationFrame(step)
  }
  function setOn(tile, active) {
    if (active) { UC.tiles.forEach(t => t !== tile && t.classList.remove('on')); UC.active = tile }
    else if (UC.active === tile) UC.active = null
    tile.classList.toggle('on', active)
    if (tile.contains(cpu)) active ? animSize(8, 16) : animSize(2, 4)
  }
  UC.tiles.forEach(t => {
    on(t, 'pointerenter', e => { if (e.pointerType === 'mouse') setOn(t, true) })
    on(t, 'pointerleave', e => { if (e.pointerType === 'mouse' && !t.contains(document.activeElement)) setOn(t, false) })
    on(t, 'focusin', () => setOn(t, true))
    on(t, 'focusout', e => { if (!t.contains(e.relatedTarget)) setOn(t, false) })
    on(t, 'pointerup', e => { if (e.pointerType !== 'mouse') setOn(t, true) })
    on(t, 'keydown', e => { if ((e.key === 'Enter' || e.key === ' ') && e.target === t) { e.preventDefault(); setOn(t, !t.classList.contains('on')) } })
  })
  const ucScene = {
    id: 'usecases', el: $('#usecases'), tall: false,
    orb: () => UC.active ? { anchor: UC.active.querySelector('.ts'), glow: 0.8, stiff: 11 } : { anchor: UC.slot, glow: 0.5 },
  }
  const ctaScene = {
    id: 'cta', el: $('#cta'), tall: false,
    orb: () => ({ anchor: $('#cta-slot'), follow: true, float: true, glow: 0.9, stiff: 6 }),
  }

  /* ---------- loop ---------- */
  const scenes = [heroScene, fsyScene, isoScene, netScene, keysScene, upScene, ucScene, ctaScene]
  scenes.forEach(s => { s.last = -1 })
  const rail = $$('.rail a')
  const boot0 = performance.now()
  let lastT = performance.now(), activeId = '', raf = 0

  function progressOf(s, vh) {
    const r = s.el.getBoundingClientRect()
    const span = r.height - (vh - hdr)
    return { r, p: s.tall && span > 0 ? clamp((hdr - r.top) / span) : 0, vis: r.bottom > 0 && r.top < vh }
  }
  function frame(now) {
    const dt = Math.min(0.05, (now - lastT) / 1000); lastT = now
    if (!hero.done) {
      hero.t = now - hero.t0
      if (hero.t >= hero.T) { hero.t = hero.T; hero.done = true }
      heroRender(hero.t)
    }
    const vh = innerHeight, mid = hdr + (vh - hdr) / 2
    let active = null, ap = 0
    for (const s of scenes) {
      const { r, p, vis } = progressOf(s, vh)
      if (vis && s.update && p !== s.last) { s.update(p); s.last = p }
      if (vis && s.frame) s.frame(now)
      if (vis && s.fx) s.fx()
      if (!active && r.top <= mid && r.bottom > mid) { active = s; ap = p }
    }
    active ||= scenes[0]
    orbFrame(active.orb(ap), dt, now)
    if (active.id !== activeId) {
      activeId = active.id
      rail.forEach(a => a.classList.toggle('on', a.dataset.s === activeId))
    }
    drawFx()
    raf = requestAnimationFrame(frame)
  }

  on(window, 'resize', () => {
    measureHdr()
    sizeFx()
    if (netDia.ensure()) { netScene.last = -1; NET.live.forEach(k => { k.el = null }) }
    if (keysDia.ensure()) keysScene.last = -1
    if (upDia.ensure()) { upScene.last = -1; UP.lastLine = 0 }
    isoScene.last = -1
  })
  const clk = $('#clock')
  const tick = () => { const d = new Date(); clk.textContent = `${pad(d.getHours())}:${pad(d.getMinutes())}` }
  tick()
  const clkId = setInterval(tick, 15000)
  sizeFx()
  paintSize()
  raf = requestAnimationFrame(frame)

  return () => {
    ac.abort()
    cancelAnimationFrame(raf)
    cancelAnimationFrame(sizeAnim)
    clearInterval(clkId)
  }
}
