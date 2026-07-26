// Loads every dashboard route in a real browser and fails on console errors,
// unhandled exceptions, or a root that never renders.
//
// This exists because neither `tsc` nor `go vet` can see the two bugs that hurt
// most here: a hook called from the wrong place, and the server bouncing SPA
// routes to "./". Both shipped clean type checks and both broke the whole app.
//
//   make smoke        build, serve, and check every route
//   SMOKE_BASE=http://127.0.0.1:5173 bun scripts/smoke.mjs   against the dev server
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { setTimeout as sleep } from 'node:timers/promises'

// Point CHROME at any Chromium build; most systems have one of these.
const CHROME =
  process.env.CHROME ??
  ['/usr/bin/chromium', '/usr/bin/google-chrome', '/home/enow/chrome-testing/chrome-linux64/chrome']
    .find((p) => existsSync(p)) ??
  'chromium'
const BASE = process.env.SMOKE_BASE ?? 'http://127.0.0.1:8788'
const ROUTES = [
  '/', '/sessions', '/models', '/tools', '/memory', '/skills', '/cron',
  '/channels', '/mcp', '/analytics', '/files', '/logs', '/config', '/system',
]

const chrome = spawn(CHROME, [
  '--headless=new', '--disable-gpu', '--no-sandbox', '--disable-dev-shm-usage',
  '--remote-debugging-port=9333', 'about:blank',
], { stdio: 'ignore' })
const shutdown = () => { try { chrome.kill() } catch {} }
process.on('exit', shutdown)

let wsURL = ''
for (let i = 0; i < 80; i++) {
  try {
    const r = await fetch('http://127.0.0.1:9333/json/version')
    wsURL = (await r.json()).webSocketDebuggerUrl
    break
  } catch { await sleep(250) }
}
if (!wsURL) { console.error('chrome did not start'); process.exit(1) }

const ws = new WebSocket(wsURL)
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej })

let id = 0
const pending = new Map()
let events = []
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data)
  if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id) }
  else if (msg.method) events.push(msg)
}
const send = (method, params = {}, sessionId) => {
  const n = ++id
  return new Promise((res) => {
    pending.set(n, res)
    ws.send(JSON.stringify({ id: n, method, params, ...(sessionId ? { sessionId } : {}) }))
  })
}

const { result: target } = await send('Target.createTarget', { url: 'about:blank' })
const { result: attached } = await send('Target.attachToTarget', { targetId: target.targetId, flatten: true })
const sid = attached.sessionId
await send('Runtime.enable', {}, sid)
await send('Page.enable', {}, sid)

const evaluate = async (expression) => {
  const { result } = await send('Runtime.evaluate', { expression, returnByValue: true }, sid)
  return result?.result?.value
}

let failures = 0
for (const route of ROUTES) {
  events = []
  await send('Page.navigate', { url: BASE + route }, sid)

  // Poll until React has painted something, rather than guessing a delay.
  let textLen = 0
  for (let i = 0; i < 60; i++) {
    await sleep(200)
    textLen = (await evaluate('document.getElementById("root")?.innerText?.trim().length ?? 0')) ?? 0
    if (textLen > 20) break
  }
  await sleep(500) // let effects settle so late errors are captured too

  const problems = []
  for (const e of events) {
    if (e.method === 'Runtime.exceptionThrown') {
      const d = e.params.exceptionDetails
      problems.push('EXCEPTION: ' + (d.exception?.description || d.text).split('\n')[0])
    }
    if (e.method === 'Runtime.consoleAPICalled' && e.params.type === 'error') {
      const text = e.params.args.map((a) => a.value ?? a.description ?? '').join(' ')
      if (/Failed to load resource|net::ERR|Failed to fetch/.test(text)) continue
      problems.push('CONSOLE: ' + text.split('\n')[0].slice(0, 150))
    }
  }
  if (textLen <= 20) problems.push(`BLANK: #root rendered ${textLen} chars`)

  if (problems.length) {
    failures++
    console.log(`FAIL ${route}`)
    for (const p of problems) console.log(`       ${p}`)
  } else {
    console.log(`ok   ${route.padEnd(12)} ${textLen} chars`)
  }
}

ws.close()
shutdown()
console.log(failures ? `\n${failures}/${ROUTES.length} route(s) with problems` : `\nAll ${ROUTES.length} routes clean`)
process.exit(failures ? 1 : 0)
