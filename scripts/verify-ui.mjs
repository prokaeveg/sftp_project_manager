// Browser-only integration checks with an isolated mock Wails bridge.
// Real JSON/path/SSH behavior is covered by Go tests, not by this mock.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { spawn, spawnSync } from 'node:child_process'
import { readFile, writeFile, mkdir, mkdtemp, rm, stat } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { fileURLToPath } from 'node:url'
const workspace = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const assets = path.join(workspace, 'frontend', 'dist')
const output = path.join(workspace, 'dist', 'ui-review')
const edgePath = process.env.SFTP_UI_BROWSER || 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe'
await stat(edgePath)
await stat(path.join(assets, 'index.html'))
await mkdir(output, { recursive: true })
const profile = await mkdtemp(path.join(os.tmpdir(), 'sftp-ui-review-'))
const server = createServer(async (request, response) => {
 try {
  const name = new URL(request.url, 'http://localhost').pathname
  const file = path.resolve(assets, '.' + (name === '/' ? '/index.html' : decodeURIComponent(name)))
  const relative = path.relative(assets, file)
  if (relative.startsWith('..') || path.isAbsolute(relative)) throw new Error('outside assets')
  response.setHeader('Content-Type', ({ '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css' })[path.extname(file)] || 'application/octet-stream')
  response.end(await readFile(file))
 } catch { response.writeHead(404); response.end() }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const browser = spawn(edgePath, ['--headless=new', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-extensions', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { windowsHide: true, stdio: 'ignore' })
let socket, connection, nextID = 0
const pending = new Map()
function delay(ms) { return new Promise(resolve => setTimeout(resolve, ms)) }
async function until(check, label, timeout = 12000) {
 const deadline = Date.now() + timeout
 while (Date.now() < deadline) {
  try { const result = await check(); if (result) return result } catch {}
  await delay(60)
 }
 throw new Error(`Timed out: ${label}`)
}
function send(method, params = {}) {
 const id = ++nextID
 return new Promise((resolve, reject) => {
  const timer = setTimeout(() => { pending.delete(id); reject(new Error(`Debugger timeout: ${method}`)) }, 12000)
  timer.unref()
  pending.set(id, { resolve: result => { clearTimeout(timer); resolve(result) }, reject: error => { clearTimeout(timer); reject(error) } })
  socket.send(JSON.stringify({ id, method, params }))
 })
}
async function evaluate(expression) {
 const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
 if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text)
 return result.result.value
}
async function click(testID) {
 await until(() => evaluate(`(() => { const e = document.querySelector('[data-testid="${testID}"]'); return !!e && !e.disabled && e.getBoundingClientRect().height > 0 && !e.closest('dialog:not([open])') })()`), `click ${testID}`)
 await evaluate(`(() => { const e = document.querySelector('[data-testid="${testID}"]'); if (!e || e.disabled) throw Error('Unavailable: ${testID}'); e.focus(); e.click() })()`)
}
async function input(testID, value) {
 await evaluate(`(() => { const e = document.querySelector('[data-testid="${testID}"]'); if (!e) throw Error('Missing: ${testID}'); e.value = ${JSON.stringify(value)}; e.dispatchEvent(new Event(e.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })) })()`)
}
async function visible(testID) {
 return evaluate(`(() => { const e = document.querySelector('[data-testid="${testID}"]'); return !!e && e.getBoundingClientRect().height > 0 && !e.closest('dialog:not([open])') })()`)
}
async function screenshot(name) {
 const result = await send('Page.captureScreenshot', { format: 'png' })
 await writeFile(path.join(output, name + '.png'), Buffer.from(result.data, 'base64'))
}

function installMock() {
 const clone = value => JSON.parse(JSON.stringify(value))
 const initial = { schemaVersion: 2, projectName: '', projects: [], activeProjectID: '', activeConnectionID: '', activeMappingID: '' }
 let settings = JSON.parse(localStorage.getItem('sftp-ui-settings') || 'null') || initial
 const calls = []
 const save = () => localStorage.setItem('sftp-ui-settings', JSON.stringify(settings))
 function flatten() {
  delete settings.profile
  const p = settings.projects.find(p => p.id === settings.activeProjectID)
  const c = p?.connections.find(c => c.id === settings.activeConnectionID)
  const m = c?.mappings.find(m => m.id === settings.activeMappingID)
  if (p && c && m) settings.profile = { name: p.name, localRoot: p.localRoot + (m.localPath === '.' ? '' : '\\' + m.localPath.replaceAll('/', '\\')), remoteRoot: m.remotePath, connection: c.connection, excludes: c.excludes || [], excludeBase: m.localPath === '.' ? '' : m.localPath }
 }
 function status() { flatten(); return clone({ platform: 'windows/amd64', dataDir: 'C:\\Demo\\data', webviewDataDir: 'C:\\Demo\\data\\webview2', webviewVersion: '154', sshPath: 'ssh.exe', sshMessage: '', settings }) }
 function select(p, connectionID, mappingID) {
  settings.activeProjectID = p.id
  const c = p.connections.find(c => c.id === (connectionID || p.defaultConnectionID)) || p.connections[0]
  const m = c?.mappings.find(m => m.id === mappingID) || c?.mappings[0]
  settings.activeConnectionID = c?.id || ''; settings.activeMappingID = m?.id || ''; settings.projectName = p.name
  save(); return status()
 }
 const entry = (name, relativePath, isDir = false, excluded = false) => ({ name, relativePath, isDir, isLink: false, excluded, size: isDir ? 0 : 128 })
 window.__uiMock = { calls, get settings() { return clone(settings) } }
 window.go = { main: { App: {
  GetStatus: async () => status(),
  SelectLocalDirectory: async () => 'C:\\Projects\\demo',
  SaveProject: async draft => {
   calls.push({ method: 'SaveProject' }); const p = clone(draft)
   p.id ||= crypto.randomUUID(); p.connections ||= []
   for (const c of p.connections) { c.id ||= crypto.randomUUID(); for (const m of c.mappings) m.id ||= crypto.randomUUID() }
   p.defaultConnectionID ||= p.connections[0]?.id || ''
   if (p.defaultConnectionID && !p.connections.some(c => c.id === p.defaultConnectionID)) throw new Error('Unknown default server')
   const index = settings.projects.findIndex(old => old.id === p.id)
   if (index < 0) settings.projects.push(p); else settings.projects[index] = p
   return select(p, index >= 0 ? settings.activeConnectionID : '', index >= 0 ? settings.activeMappingID : '')
  },
  SelectProject: async (projectID, connectionID, mappingID) => select(settings.projects.find(p => p.id === projectID), connectionID, mappingID),
  ListProjectLocal: async relative => {
   calls.push({ method: 'ListProjectLocal', relative })
   return relative === '.' ? [entry('src', 'src', true), entry('README.md', 'README.md'), entry('.env', '.env', false, !!settings.profile)] : [entry('App.vue', relative + '/App.vue')]
  },
  ListRemote: async relative => { calls.push({ method: 'ListRemote', relative }); return [entry('server-file.txt', relative === '.' ? 'server-file.txt' : relative + '/server-file.txt')] },
  DiscoverSSH: async configPath => ({ configPath, aliases: [], warnings: [] }),
  EvaluateSSH: async () => ({ hostname: 'fixture', user: 'demo', port: 22, identityFiles: [], proxyJump: '', hasProxyCommand: false, configPath: '', alias: 'fixture' }),
  CancelOperation: async () => {},
  PreviewBatch: async (direction, relatives) => {
   calls.push({ method: 'PreviewBatch', direction, relatives })
   const profile = settings.profile
   return { token: 'fixture-token', direction, server: profile.connection.alias || profile.connection.host, fileCount: relatives.length, totalBytes: relatives.length * 128,
    items: relatives.map(relativePath => ({ relativePath, source: direction === 'upload' ? profile.localRoot + '\\' + relativePath : profile.remoteRoot + '/' + relativePath, destination: direction === 'upload' ? profile.remoteRoot + '/' + relativePath : profile.localRoot + '\\' + relativePath, kind: 'file', size: 128, exists: false, skippedReason: '' })) }
  },
  ExecuteBatch: async () => ({ items: [{ relativePath: 'README.md', kind: 'file', status: 'completed', error: '' }] })
 } } }
}

try {
 const port = await until(async () => (await readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0], 'browser debugger')
 const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
 connection = pages.find(page => page.type === 'page')
 assert.ok(connection, 'browser page')
 socket = new WebSocket(connection.webSocketDebuggerUrl)
 await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }) })
 socket.addEventListener('message', event => {
  const message = JSON.parse(event.data), action = pending.get(message.id)
  if (!action) return
  pending.delete(message.id)
  if (message.error) action.reject(new Error(message.error.message)); else action.resolve(message.result)
 })
 await send('Page.enable')
 await send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false })
 await send('Page.addScriptToEvaluateOnNewDocument', { source: `(${installMock.toString()})()` })
 await send('Page.navigate', { url: `http://127.0.0.1:${server.address().port}/` })
 await until(() => visible('create-project'), 'welcome')
 await screenshot('01-welcome')
 if (!await visible('project-name')) await click('create-project')
 await until(() => visible('project-name'), 'project wizard')
 await input('project-name', 'Demo project')
 await click('project-folder-picker')
 await until(() => evaluate(`document.querySelector('[data-testid="project-folder"]').value === 'C:\\\\Projects\\\\demo'`), 'folder selection')
 await click('project-save')
 await until(async () => await visible('local-pane') && await evaluate(`document.querySelector('[data-testid="local-pane"]').textContent.includes('README.md')`), 'local project tree')
 assert.equal(await evaluate('window.__uiMock.settings.projects[0].connections.length'), 0)
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 0, 'no startup SSH')
 await screenshot('02-local-project')
 // Settings interaction checks are below; test IDs are intentionally stable UI contracts.
 await click('add-server')
 await until(() => visible('settings-dialog'), 'server settings')
 await click('settings-add-server')
 await input('server-name', 'Development')
 await input('server-alias', 'demo-dev')
 await input('remote-root', '/srv/demo')
 assert.ok(await evaluate(`document.querySelector('[data-testid="default-server-select"]').getBoundingClientRect().width`) < 25, 'compact default radio')
 await screenshot('03-server-settings')
 await click('settings-save')
 await until(() => evaluate('window.__uiMock.settings.projects[0].connections.length === 1'), 'first server saved')
 assert.equal(await evaluate('window.__uiMock.settings.projects[0].defaultConnectionID === window.__uiMock.settings.projects[0].connections[0].id'), true, 'first server default')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 0, 'saving server must not open SSH')
 await click('settings-open')
 await until(() => visible('settings-dialog'), 'reopen server settings')
 await click('settings-add-server')
 await input('server-name', 'Production')
 await input('server-alias', 'demo-prod')
 await input('remote-root', '/srv/production')
 await click('default-server-select')
 await click('settings-save')
 await until(() => evaluate('window.__uiMock.settings.projects[0].connections.length === 2'), 'two servers saved')
 assert.equal(await evaluate('window.__uiMock.settings.projects[0].defaultConnectionID === window.__uiMock.settings.projects[0].connections[1].id'), true, 'user selected default')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 0)
 const productionID = await evaluate('window.__uiMock.settings.projects[0].connections[1].id')
 await input('server-select', productionID)
 await until(() => evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('/srv/production')`), 'server root switched')
 await click('connect-server')
 await until(() => evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('server-file.txt')`), 'remote file tree')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 1, 'one explicit remote read')
 const layout = await evaluate(`(() => {
  const left = document.querySelector('[data-testid="local-pane"]').getBoundingClientRect()
  const right = document.querySelector('[data-testid="remote-pane"]').getBoundingClientRect()
  return { left: { x: left.x, width: left.width, height: left.height }, right: { x: right.x, width: right.width, height: right.height }, bodyWidth: document.body.scrollWidth, viewport: innerWidth }
 })()`)
 assert.ok(layout.left.x < layout.right.x && layout.left.width < layout.right.width && layout.left.height > 500 && layout.right.height > 500, 'IDE sidebar and two full-height panels')
 assert.ok(layout.bodyWidth <= layout.viewport, 'no horizontal page overflow')
 await screenshot('04-two-trees')
 await evaluate(`(() => { const pane = document.querySelector('[data-testid="local-pane"]'); const checkbox = [...pane.querySelectorAll('input[type="checkbox"]')].find(e => e.getAttribute('aria-label')?.includes('README.md')); checkbox.click() })()`)
 await evaluate(`document.querySelector('[data-testid="local-pane"] .tree-toolbar button.primary').click()`)
 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'batch preview modal')
 assert.ok(await evaluate(`document.querySelector('dialog.preview-modal').textContent.includes('/srv/production/README.md')`), 'preview destination from active server')
 await screenshot('06-transfer-preview')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'Escape closes preview without empty modal')
 // Escape must discard changes and restore keyboard focus to the opener.
 await click('settings-open')
 await input('server-name', 'Unsaved name')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(async () => !await visible('settings-dialog'), 'Escape closes settings')
 assert.equal(await evaluate(`window.__uiMock.settings.projects[0].connections.some(c => c.name === 'Unsaved name')`), false, 'cancel does not save draft')
 // Cancelling a new-project wizard must leave existing-project settings usable.
 await click('create-project')
 await until(() => visible('project-name'), 'new project wizard')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(async () => !await visible('settings-dialog'), 'cancel new-project wizard')
 await click('settings-open')
 await until(() => visible('server-name'), 'existing project settings after wizard cancellation')
 await screenshot('07-two-server-settings')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(async () => !await visible('settings-dialog'), 'close settings')
 const firstProjectID = await evaluate('window.__uiMock.settings.projects[0].id')
 await click('create-project')
 await input('project-name', 'Second local project')
 await click('project-folder-picker')
 await click('project-save')
 await until(() => evaluate(`window.__uiMock.settings.projects.length === 2`), 'local-only project after server project')
 assert.equal(await evaluate('window.__uiMock.settings.projects[1].connections.length'), 0)
 assert.equal(await evaluate('window.__uiMock.settings.projects[1].defaultConnectionID || ""'), '')
 await input('project-select', firstProjectID)
 await until(() => evaluate(`window.__uiMock.settings.activeProjectID === ${JSON.stringify(firstProjectID)}`), 'switch project back')
 assert.equal(await evaluate('window.__uiMock.settings.activeConnectionID'), productionID, 'reopening project uses default')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 1, 'switching projects must not start SSH')
 await send('Page.reload')
 await until(() => visible('local-pane'), 'restore project')
 assert.equal(await evaluate('window.__uiMock.settings.projects[0].defaultConnectionID'), productionID, 'default persisted in mock storage')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 0, 'reload never auto connects')
 await send('Emulation.setDeviceMetricsOverride', { width: 960, height: 640, deviceScaleFactor: 1, mobile: false })
 assert.ok(await evaluate(`document.querySelector('[data-testid="server-select"]').getBoundingClientRect().width`) >= 100, 'server dropdown readable at minimum width')
 assert.ok(await evaluate(`document.querySelector('[data-testid="remote-pane"] .tree-row').getBoundingClientRect().bottom < innerHeight - 20`), 'disconnected server root visible without scrolling')
 await screenshot('05-minimum-window')
 console.log('Browser checks passed: local-only creation, folder selection, two servers/default, explicit SSH, two panels, transfer preview, Escape/discard, second project/default switching, restored project, minimum-size layout. Screenshots:', output)
} finally {
 if (socket?.readyState === WebSocket.OPEN) {
  try { await Promise.race([send('Browser.close'), delay(1500)]) } catch {}
  socket.close()
 }
 if (browser.exitCode === null) {
  await Promise.race([new Promise(resolve => browser.once('exit', resolve)), delay(2000)])
  if (browser.exitCode === null) spawnSync('taskkill.exe', ['/PID', String(browser.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' })
 }
 await new Promise(resolve => server.close(resolve))
 // Delete only the exact temporary profile created above, after its browser exits.
 if (path.dirname(path.resolve(profile)) !== path.resolve(os.tmpdir()) || !path.basename(profile).startsWith('sftp-ui-review-')) throw new Error('Unsafe temporary profile path')
 await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 })
}
