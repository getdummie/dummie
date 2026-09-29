<script setup lang="ts">
import '~/assets/css/story.css'
import { mountStory } from '~/lib/story.js'

const { consoleUrl } = useRuntimeConfig().public

const description = 'Your own computer in the cloud. Ready in seconds, sealed off from everything else, and yours for as long as you need.'

useHead({ title: 'dummie — secure computers for everyone' })
useSeoMeta({
  description,
  ogTitle: 'dummie — secure computers for everyone',
  ogDescription: description,
})

const root = ref<HTMLElement | null>(null)
let unmount: (() => void) | undefined
onMounted(() => { if (root.value) unmount = mountStory(root.value) })
onBeforeUnmount(() => unmount?.())

const others = [
  { id: 'vm-b', label: 'vm-c21e', bars: ['54%', '80%'] },
  { id: 'vm-c', label: 'vm-90b4', bars: ['64%', '38%'] },
]
const chapters = [
  ['hero', 'PID 1'],
  ['fsy', 'Fast · Safe · Yours'],
  ['isolation', 'Isolation'],
  ['network', 'Network'],
  ['keys', 'Keys'],
  ['upgrades', 'Upgrades'],
  ['usecases', 'What it\'s for'],
  ['cta', 'Try now'],
]
</script>

<template>
  <div ref="root" class="story">
    <div id="orb-light" aria-hidden="true" />

    <div class="main">
      <section id="hero" class="hero" aria-labelledby="hero-h">
        <div class="grid-bg" aria-hidden="true" />
        <div class="wrap">
          <div class="hero-grid">
            <div>
              <p class="eyebrow rise">Network and hypervisor isolated</p>
              <h1 id="hero-h" class="rise" style="animation-delay:.06s">Secure computers<span class="soft">for everyone</span></h1>
              <div class="ctas rise" style="animation-delay:.18s">
                <div class="row">
                  <a class="btn btn-primary" :href="`${consoleUrl}/signin`">Boot a machine <span class="arr" aria-hidden="true">→</span></a>
                  <a class="btn btn-outline" href="#usecases">What it's for</a>
                </div>
                <NuxtLink class="techlink" to="/architecture"><span aria-hidden="true">&gt;_</span> Talk tech to me</NuxtLink>
              </div>
            </div>

            <div class="machine-wrap">
              <div id="badge" class="badge"><span id="badge-slot" class="badge-slot" /><span class="chip"><span class="dot" /><span id="badge-txt">my-app · booting</span></span></div>
              <div class="machine" role="img" aria-label="A machine boots: a terminal runs bun dev, reports ready in 412 ms on local port 8000, and a browser opens the live app at my-app.dummie.app.">
                <div class="screen">
                  <div class="desk">
                    <div class="sbar">
                      <span>Activities</span>
                      <span id="clock" class="tabular">09:24</span>
                      <span class="ic" aria-hidden="true">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M2 9a15 15 0 0 1 20 0M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0" /><circle cx="12" cy="19.5" r="1" fill="currentColor" /></svg>
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="7" width="17" height="10" rx="2" /><path d="M22 11v2" /><rect x="4.5" y="9.5" width="12" height="5" fill="currentColor" stroke="none" /></svg>
                      </span>
                    </div>
                    <div id="win-browser" class="win browser">
                      <div class="tabs"><span class="tab"><i />My app</span></div>
                      <div class="urlbar">
                        <span class="nv">‹ ›</span>
                        <span id="url" class="url"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4"><rect x="5" y="11" width="14" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 8 0v4" /></svg><span id="url-txt" /><span id="url-caret" class="caret" /></span>
                      </div>
                      <div id="loadbar" class="loadbar" />
                      <div class="page">
                        <div id="sk" class="sk"><span style="height:1em;width:40%" /><span style="height:6.5em" /><span style="height:3.4em" /></div>
                        <div id="real" class="real">
                          <div class="top"><i />Acme<em><span>Docs</span><span>Pricing</span></em></div>
                          <div class="hb"><strong>Ship it fast.</strong><small>Built this morning.</small><b>Get started</b></div>
                          <div class="cards"><div>Fast</div><div>Safe</div><div>Yours</div></div>
                        </div>
                      </div>
                    </div>
                    <div id="win-term" class="win term">
                      <div class="th"><i /><i /><i /><span>bash — my-app</span></div>
                      <div class="tb">
                        <div id="t-cmd" />
                        <div id="t-ready" class="d" />
                        <div id="t-local" class="g" />
                        <div id="t-prompt" />
                      </div>
                    </div>
                  </div>
                  <div id="boot" class="boot">
                    <div class="boot-in">
                      <span id="boot-slot" class="boot-slot" />
                      <div id="boot-log" class="boot-log">
                        <div><b>[ 0.000]</b> microVM starting</div>
                        <div><b>[ 0.094]</b> own kernel up</div>
                        <div><b>[ 0.162]</b> dinit is pid 1</div>
                        <div><b>[ 0.241]</b> answering ssh</div>
                      </div>
                      <div class="boot-bar"><i id="boot-bar" /></div>
                    </div>
                  </div>
                </div>
              </div>
              <button id="skip" class="skip" type="button">Skip intro ↵</button>
            </div>
          </div>
        </div>
      </section>

      <section id="fsy" class="scene" style="--len:340vh" aria-label="Fast, safe, yours">
        <div class="stage">
          <div class="wrap fsy-grid">
            <div class="fsy-words">
              <div class="w on"><span class="word">Fast<b>.</b></span><div class="wc"><span>Booted in seconds.</span></div></div>
              <div class="w"><span class="word">Safe<b>.</b></span><div class="wc"><span>Sealed at the hardware. Its own kernel, filesystem and network stack.</span></div></div>
              <div class="w"><span class="word">Yours<b>.</b></span><div class="wc"><span>Root is handed to you. Keep it for as long as you need.</span></div></div>
            </div>
            <div class="fsy-vis" aria-hidden="true">
              <svg id="fsy-svg" viewBox="0 0 400 400" />
              <span id="fsy-slot" class="fsy-slot" />
              <div id="fsy-timer" class="fsy-timer"><strong id="fsy-time">0.00 s</strong><small id="fsy-tl">BOOTING</small></div>
              <div id="fsy-seal" class="fsy-seal">SEALED · KVM</div>
              <div id="fsy-you" class="fsy-you"><i>you</i><span id="fsy-you-t">waiting for a key</span></div>
            </div>
          </div>
        </div>
      </section>

      <section id="isolation" class="scene" style="--len:500vh" aria-labelledby="iso-h">
        <div class="stage">
          <div class="wrap iso-grid">
            <div class="iso-copy">
              <p class="eyebrow">Isolation</p>
              <h2 id="iso-h" class="h2">Its own kernel. A hardware boundary.</h2>
              <p class="lede">Your machine is a real VM, not a container. There is no shared filesystem and no shared process list.</p>
              <ol id="iso-steps" class="steps">
                <li>A physical server.</li>
                <li>KVM turns its hardware into boundaries.</li>
                <li>Each VM boots its own kernel.</li>
                <li>A reach for a neighbour stops at KVM.</li>
                <li>Delete the machine. The disk goes with it.</li>
              </ol>
            </div>
            <div class="iso-vis" aria-hidden="true">
              <div class="vms">
                <div id="vm-yours" class="vm yours">
                  <div class="vm-h"><span>yours</span><span class="id">vm-7f3a</span></div>
                  <div class="vm-body"><span id="iso-slot" class="slot" /></div>
                  <div class="bars"><i style="width:72%" /><i style="width:46%" /></div>
                  <div class="kern">own kernel</div>
                  <div id="del" class="del">Delete machine</div>
                </div>
                <div v-for="o in others" :id="o.id" :key="o.id" class="vm">
                  <div class="vm-h"><span>someone else</span><span class="id">{{ o.label }}</span></div>
                  <div class="vm-body">
                    <svg class="other-orb" viewBox="0 0 100 100"><circle class="s" cx="50" cy="50" r="48" /><circle class="k" cx="50" cy="50" r="40" /><polygon class="e" points="50,29 68,39 50,49 32,39" /></svg>
                  </div>
                  <div class="bars"><i :style="{ width: o.bars[0] }" /><i :style="{ width: o.bars[1] }" /></div>
                  <div class="kern">own kernel</div>
                </div>
                <div id="ghost" class="ghost">deleted<br>disk gone too</div>
                <span id="fin1" class="fin f1" />
                <span id="fin2" class="fin f2" />
                <span id="probe" class="probe" />
                <span id="spark" class="spark" />
                <span id="blocked" class="blocked">stopped by KVM</span>
                <svg id="ptr" class="ptr" viewBox="0 0 24 24"><path d="M4 2l16 10-7 1.5L9.5 21z" /></svg>
              </div>
              <div id="kvm" class="kvm"><b>KVM</b><span>isolation in hardware</span></div>
              <div id="server" class="server">
                <div class="disks">
                  <div id="disk-yours" class="disk mine"><svg viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5" /></svg><span>disk · vm-7f3a</span></div>
                  <div v-for="o in others" :key="o.id" class="disk"><svg viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5" /></svg><span>disk · {{ o.label }}</span></div>
                </div>
                <div class="chassis"><span class="leds"><i /><i /><i /></span><span>physical server</span><span class="vent" /><span>x86_64</span></div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section id="network" class="scene" style="--len:520vh" aria-labelledby="net-h">
        <div class="stage">
          <div class="wrap flow-wrap">
            <div class="flow-head">
              <div>
                <p class="eyebrow">Network</p>
                <h2 id="net-h" class="h2">Decide what gets in or out.</h2>
                <p class="lede">A machine talks to nothing until you say so. Every attempt is logged, allowed or blocked.</p>
              </div>
              <p id="net-cap" class="cap" aria-live="polite" />
            </div>
            <div class="net-body">
              <div id="net-dia" class="dia-host" data-label="Packets leave the machine and meet a policy gate. deb.debian.org passes, github.com is refused, unknown traffic is dropped." />
              <div class="net-side">
                <div class="pnl">
                  <div class="pnl-h"><span>policy · my-app</span><span id="uptime" class="tabular">up 00:14:07</span></div>
                  <div id="rule-deb" class="rule"><span>deb.debian.org</span><button id="sw-deb" class="sw" type="button" role="switch" aria-checked="true" aria-label="Allow deb.debian.org"><i /></button><span id="rv-deb" class="rv a">allow</span><button class="send" type="button" data-d="0" aria-label="Send a packet to deb.debian.org">send</button></div>
                  <div id="rule-gh" class="rule"><span>github.com</span><button id="sw-gh" class="sw" type="button" role="switch" aria-checked="false" aria-label="Allow github.com"><i /></button><span id="rv-gh" class="rv b">block</span><button class="send" type="button" data-d="1" aria-label="Send a packet to github.com">send</button></div>
                  <div class="rule"><span>everything else</span><span class="lock" aria-hidden="true">—</span><span class="rv b">drop</span><button class="send" type="button" data-d="2" aria-label="Send a packet to an unknown address">send</button></div>
                  <div id="net-hint" class="hint">Your turn. Flip a rule, then send a packet.</div>
                </div>
                <div class="pnl">
                  <div class="pnl-h"><span>egress log</span><span class="live">live</span></div>
                  <ol id="net-log" class="log" aria-live="polite" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section id="keys" class="scene" style="--len:500vh" aria-labelledby="keys-h">
        <div class="stage">
          <div class="wrap flow-wrap">
            <div class="flow-head">
              <div>
                <p class="eyebrow">Integrations</p>
                <h2 id="keys-h" class="h2">Keys never touch the VM.</h2>
                <p class="lede">Clone a private repo or call ChatGPT and Claude from inside the machine. The credential is added on the way out, by the host.</p>
              </div>
              <p id="keys-cap" class="cap" aria-live="polite" />
            </div>
            <div id="keys-dia" class="dia-host" data-label="A git clone leaves the VM with no credential, passes through intproxy on the host, gains a short-lived token from dclient, and reaches github.com. Calls to ChatGPT and Claude take the same path. The VM holds zero credentials throughout." />
          </div>
        </div>
      </section>

      <section id="upgrades" class="scene" style="--len:440vh" aria-labelledby="up-h">
        <div class="stage">
          <div class="wrap flow-wrap">
            <div class="flow-head">
              <div>
                <p class="eyebrow">Upgrades</p>
                <h2 id="up-h" class="h2">Upgrades don't drop you.</h2>
                <p class="lede">dpipe owns the connection, not dproxy. Shipping a new dproxy disconnects nobody.</p>
              </div>
              <p id="up-cap" class="cap" aria-live="polite" />
            </div>
            <div id="up-dia" class="dia-host" data-label="An SSH session runs from you through dpipe to the VM. dproxy is replaced by a new version while the session stays connected." />
            <div class="stats">
              <span>session <b class="g">connected</b></span>
              <span>connected for <b id="up-timer">00:00:00</b></span>
              <span>bytes dropped <b>0</b></span>
              <span>dproxy <b id="up-ver">v1</b></span>
            </div>
          </div>
        </div>
      </section>

      <section id="usecases" class="uc-sec" aria-labelledby="uc-h">
        <div class="wrap">
          <div class="uc-head">
            <h2 id="uc-h" class="eyebrow">What it's for</h2>
            <span id="uc-slot" class="uc-slot" aria-hidden="true" />
          </div>
          <ul class="uc-grid">
            <li class="uc" tabindex="0" aria-labelledby="uc1">
              <div class="vg v-term" aria-hidden="true">
                <div>$ bun dev<span class="cursor" /></div>
                <div class="l d" style="--i:1">ready in 412 ms</div>
                <div class="l g" style="--i:2">➜ local  :8000</div>
                <div class="l g" style="--i:3">➜ share  my-app.dummie.app</div>
                <div class="l" style="--i:4">$</div>
              </div>
              <div class="uc-meta"><div><h3 id="uc1">Web development</h3><p>Build on a machine that isn't your laptop.</p></div><span class="ts" /></div>
            </li>
            <li class="uc" tabindex="0" aria-labelledby="uc2">
              <div class="vg v-lab" aria-hidden="true">
                <svg viewBox="0 0 320 200">
                  <path class="e" pathLength="1" style="--i:0" d="M160 100 L60 44" />
                  <path class="e bad" pathLength="1" style="--i:1" d="M160 100 L262 40" />
                  <path class="e" pathLength="1" style="--i:2" d="M160 100 L50 150" />
                  <path class="e bad" pathLength="1" style="--i:3" d="M160 100 L272 150" />
                  <path class="e" pathLength="1" style="--i:4" d="M160 100 L160 178" />
                  <g class="nd" style="--i:0"><circle cx="60" cy="44" r="5" /><text x="60" y="30" text-anchor="middle">example.org:443</text></g>
                  <g class="nd bad" style="--i:1"><circle cx="262" cy="40" r="5" /><text x="262" y="26" text-anchor="middle">203.0.113.9:4444</text></g>
                  <g class="nd" style="--i:2"><circle cx="50" cy="150" r="5" /><text x="50" y="170" text-anchor="middle">dns lookup</text></g>
                  <g class="nd bad" style="--i:3"><circle cx="272" cy="150" r="5" /><text x="272" y="170" text-anchor="middle">198.51.100.4:6667</text></g>
                  <g class="nd" style="--i:4"><circle cx="160" cy="178" r="5" /><text x="176" y="182">update.example.com</text></g>
                  <circle class="core" cx="160" cy="100" r="15" />
                  <text class="strong" x="160" y="126" text-anchor="middle">sample.bin</text>
                  <text class="cnt" x="12" y="192">5 traced · 2 blocked</text>
                </svg>
              </div>
              <div class="uc-meta"><div><h3 id="uc2">Malware analysis</h3><p>Open something risky, watch what it reaches for, then delete the machine.</p></div><span class="ts" /></div>
            </li>
            <li class="uc" tabindex="0" aria-labelledby="uc3">
              <div class="vg v-term v-eval" aria-hidden="true">
                <span class="sb">SANDBOX</span>
                <div>$ python solution.py</div>
                <div class="l d" style="--i:1">running 12 tests</div>
                <div class="prog"><i /></div>
                <div class="l" style="--i:4">12 passed in 0.84s</div>
                <span class="exit">exit 0</span>
              </div>
              <div class="uc-meta"><div><h3 id="uc3">Sandboxed evaluation</h3><p>Let your agent run the code it just wrote.</p></div><span class="ts" /></div>
            </li>
            <li class="uc" tabindex="0" aria-labelledby="uc4">
              <div class="vg v-share" aria-hidden="true">
                <div class="pill"><span><em>https://</em>pitch-deck.dummie.app</span><span class="cp"><span class="a">Copy link</span><span class="b">Copied</span></span></div>
                <div class="who"><span class="av"><i style="--i:0;background:#48D597">Y</i><i style="--i:1;background:#F4C76A">A</i><i style="--i:2;background:#92A7A1">B</i></span><span><b>live</b> · shared as a link</span></div>
              </div>
              <div class="uc-meta"><div><h3 id="uc4">Demos</h3><p>Share what you made as a link. It opens in any browser.</p></div><span class="ts" /></div>
            </li>
            <li class="uc" tabindex="0" aria-labelledby="uc5">
              <div class="vg v-desk" aria-hidden="true">
                <div class="bar" />
                <div class="wn"><i /><i /><i /></div>
                <div class="dock"><i /><i /><i /><i /></div>
                <span class="ring" />
                <svg class="mouse" viewBox="0 0 24 24"><path d="M4 2l16 10-7 1.5L9.5 21z" fill="#48D597" stroke="#0B1A1E" stroke-width="1.2" /></svg>
              </div>
              <div class="uc-meta"><div><h3 id="uc5">Desktops</h3><p>A full desktop, right in your browser.</p></div><span class="ts" /></div>
            </li>
            <li class="uc" tabindex="0" aria-labelledby="uc6">
              <div class="vg v-any">
                <label for="uc-cpu">vCPU <input id="uc-cpu" type="range" min="1" max="16" value="2"><output id="uc-cpu-o">2</output></label>
                <label for="uc-mem">memory <input id="uc-mem" type="range" min="1" max="64" value="4"><output id="uc-mem-o">4 GB</output></label>
                <span id="uc-sum" class="sum">it's a computer · <b>2 vCPU / 4 GB</b></span>
              </div>
              <div class="uc-meta"><div><h3 id="uc6">Anything else</h3><p>It's a computer. Size it and use it however you like.</p></div><span class="ts" /></div>
            </li>
          </ul>
        </div>
      </section>

      <section id="cta" class="cta" aria-labelledby="cta-h">
        <div class="grid-bg" aria-hidden="true" />
        <div class="wrap" style="position:relative">
          <div id="cta-slot" class="cta-slot" aria-hidden="true" />
          <h2 id="cta-h">Try now<b>.</b></h2>
          <div class="row">
            <a class="btn btn-primary" :href="`${consoleUrl}/signin`">Open the console <span class="arr" aria-hidden="true">→</span></a>
            <NuxtLink class="techlink" to="/architecture"><span aria-hidden="true">&gt;_</span> Talk tech to me</NuxtLink>
          </div>
          <p class="idle">my-app · idle · ready when you are</p>
        </div>
      </section>
    </div>

    <canvas id="fx" aria-hidden="true" />
    <div id="orb" aria-hidden="true">
      <div id="orb-halo" class="halo" />
      <svg viewBox="0 0 100 100">
        <defs>
          <radialGradient id="orb-g" cx="36%" cy="30%" r="75%">
            <stop offset="0" stop-color="#A6F3CD" />
            <stop offset=".42" stop-color="#48D597" />
            <stop offset="1" stop-color="#1E7A55" />
          </radialGradient>
        </defs>
        <circle cx="50" cy="50" r="48" fill="url(#orb-g)" />
        <g id="orb-eye">
          <circle cx="50" cy="50" r="40" fill="none" stroke="#0B1A1E" stroke-width="4.5" />
          <g id="orb-pupil">
            <line x1="31" y1="40" x2="69" y2="40" stroke="#0B1A1E" stroke-width="1.4" />
            <g id="orb-lid">
              <polygon points="50,29.5 69,40 50,50.5 31,40" fill="#0B1A1E" />
              <line x1="50" y1="29.5" x2="50" y2="50.5" stroke="#0B1A1E" stroke-width="1.4" />
            </g>
          </g>
        </g>
        <ellipse cx="36" cy="24" rx="13" ry="6.5" fill="#fff" opacity=".28" transform="rotate(-24 36 24)" />
      </svg>
    </div>

    <ol class="rail" aria-label="Chapters">
      <li v-for="[id, label] in chapters" :key="id"><a :href="`#${id}`" :data-s="id"><span>{{ label }}</span><i /></a></li>
    </ol>
  </div>
</template>
