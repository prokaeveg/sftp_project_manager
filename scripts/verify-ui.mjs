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
async function check(testID, checked) {
 await evaluate(`(() => { const e = document.querySelector('[data-testid="${testID}"]'); if (!e) throw Error('Missing: ${testID}'); e.checked = ${!!checked}; e.dispatchEvent(new Event('change', { bubbles: true })) })()`)
}
async function visible(testID) {
 return evaluate(`(() => { const e = document.querySelector('[data-testid="${testID}"]'); return !!e && e.getBoundingClientRect().height > 0 && !e.closest('dialog:not([open])') })()`)
}
async function clickTreeName(side, name, modifiers = {}) {
 const pane = side === 'local' ? 'local-pane' : 'remote-pane'
 await evaluate(`(() => {
  const pane = document.querySelector('[data-testid="${pane}"]')
  const name = [...pane.querySelectorAll('.tree-name')].find(e => e.childNodes[e.childNodes.length - 1]?.textContent.trim().replace(/\\/$/, '') === ${JSON.stringify(name)})
  if (!name) throw Error('Missing tree entry: ' + ${JSON.stringify(name)})
  name.dispatchEvent(new MouseEvent('click', { bubbles: true, ctrlKey: ${!!modifiers.ctrlKey}, metaKey: ${!!modifiers.metaKey}, shiftKey: ${!!modifiers.shiftKey} }))
 })()`)
}
async function contextTreeName(side, name) {
 const pane = side === 'local' ? 'local-pane' : 'remote-pane'
 await evaluate(`(() => {
  const pane = document.querySelector('[data-testid="${pane}"]')
  const name = [...pane.querySelectorAll('.tree-name')].find(e => e.childNodes[e.childNodes.length - 1]?.textContent.trim().replace(/\\/$/, '') === ${JSON.stringify(name)})
  if (!name) throw Error('Missing tree entry: ' + ${JSON.stringify(name)})
  name.closest('.tree-row').dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 420, clientY: 220, button: 2 }))
 })()`)
 await until(() => visible('transfer-menu'), 'transfer context menu')
}
async function selectedTreeNames(side) {
 const pane = side === 'local' ? 'local-pane' : 'remote-pane'
 return evaluate(`[...document.querySelectorAll('[data-testid="${pane}"] .tree-row[aria-selected="true"] .tree-name')].map(e => e.childNodes[e.childNodes.length - 1]?.textContent.trim().replace(/\\/$/, ''))`)
}
async function screenshot(name) {
 const result = await send('Page.captureScreenshot', { format: 'png' })
 await writeFile(path.join(output, name + '.png'), Buffer.from(result.data, 'base64'))
}

function installMock() {
 const clone = value => JSON.parse(JSON.stringify(value))
 const initial = { schemaVersion: 3, projectName: '', projects: [], activeProjectID: '', activeConnectionID: '', activeMappingID: '' }
 let settings = JSON.parse(localStorage.getItem('sftp-ui-settings') || 'null') || initial
 const calls = []
	const authResponses = []
	const forgetPasswordCalls = []
	let armedAuthPrompt = null, activeAuthPrompt = null, pendingAuthResolve = null, pendingAuthReject = null
	const queue = { paused: false, jobs: [] }
	let queueNextID = 0, lastPreview
	let navigationState = 'disconnected', navigationKey = ''
	function currentNavigationKey() { return JSON.stringify([settings.activeProjectID, settings.activeConnectionID, settings.projects.find(p => p.id === settings.activeProjectID)?.connections.find(c => c.id === settings.activeConnectionID)?.connection]) }
	function navigationStatus() {
	 if (navigationKey !== currentNavigationKey()) navigationState = 'disconnected'
	 return { state: navigationState, projectID: settings.activeProjectID, connectionID: settings.activeConnectionID }
	}
 const save = () => localStorage.setItem('sftp-ui-settings', JSON.stringify(settings))
 function remoteDestination(c, m) { return c.rootPath.replace(/\/$/, '') + (m.deploymentPath === '.' ? '' : '/' + m.deploymentPath) || '/' }
 function flatten() {
  delete settings.profile
  const p = settings.projects.find(p => p.id === settings.activeProjectID)
  const c = p?.connections.find(c => c.id === settings.activeConnectionID)
  const m = c?.mappings.find(m => m.id === settings.activeMappingID)
  if (p && c && m) settings.profile = { name: p.name, localRoot: p.localRoot + (m.localPath === '.' ? '' : '\\' + m.localPath.replaceAll('/', '\\')), remoteRoot: remoteDestination(c, m), connection: c.connection, excludes: c.excludes || [], excludeBase: m.localPath === '.' ? '' : m.localPath }
 }
 function profileFor(projectID, connectionID, mappingID) {
  const p = settings.projects.find(project => project.id === projectID)
  const c = p?.connections.find(connection => connection.id === connectionID)
  const m = c?.mappings.find(mapping => mapping.id === mappingID)
  if (!p || !c || !m) throw Error('Unknown project/server/mapping snapshot')
  return { name: p.name, localRoot: p.localRoot + (m.localPath === '.' ? '' : '\\' + m.localPath.replaceAll('/', '\\')), remoteRoot: remoteDestination(c, m), connection: c.connection, excludes: c.excludes || [], excludeBase: m.localPath === '.' ? '' : m.localPath }
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
	function startNext() {
	 if (queue.paused || queue.jobs.some(job => job.status === 'running')) return
	 const job = queue.jobs.find(job => job.status === 'waiting')
	 if (job) { job.status = 'running'; job.items[0].status = 'running' }
	}
	window.__uiMock = { calls, authResponses, forgetPasswordCalls, get settings() { return clone(settings) }, get queue() { return clone(queue) },
	 armAuthPrompt(id, prompt, canRemember = true) { armedAuthPrompt = { id, prompt, canRemember }; activeAuthPrompt = null },
	 replaceAuthPrompt(id, prompt, canRemember = true) { activeAuthPrompt = { id, prompt, canRemember } },
	 get authPending() { return !!pendingAuthResolve },
	 get navigation() { return navigationStatus() }, dropNavigation() { navigationState = 'disconnected'; navigationKey = '' },
	 progressJob(id, bytes) { const job = queue.jobs.find(job => job.id === id); job.transferredBytes = bytes; job.items[0].transferredBytes = bytes },
	 finishJob(id, status = 'completed') {
	  const job = queue.jobs.find(job => job.id === id)
	  job.status = status; job.processedItems = job.items.length
	  job.transferredBytes = status === 'completed' ? job.totalBytes : 64
	  for (const item of job.items) { item.status = status; item.transferredBytes = job.transferredBytes; item.error = status === 'failed' ? 'Fixture transfer error' : '' }
	  startNext()
	 }
	}
 window.go = { main: { App: {
  GetStatus: async () => status(),
  SelectLocalDirectory: async () => 'C:\\Projects\\demo',
  SaveProject: async draft => {
   calls.push({ method: 'SaveProject', draft: clone(draft) }); const p = clone(draft)
   p.id ||= crypto.randomUUID(); p.connections ||= []
   for (const c of p.connections) { c.id ||= crypto.randomUUID(); for (const m of c.mappings) m.id ||= crypto.randomUUID() }
   p.defaultConnectionID ||= p.connections[0]?.id || ''
   if (p.defaultConnectionID && !p.connections.some(c => c.id === p.defaultConnectionID)) throw new Error('Unknown default server')
   const index = settings.projects.findIndex(old => old.id === p.id)
   if (index < 0) settings.projects.push(p); else settings.projects[index] = p
   return select(p, index >= 0 ? settings.activeConnectionID : '', index >= 0 ? settings.activeMappingID : '')
  },
  SelectProject: async (projectID, connectionID, mappingID) => { calls.push({ method: 'SelectProject', projectID, connectionID, mappingID }); return select(settings.projects.find(p => p.id === projectID), connectionID, mappingID) },
  ListProjectLocal: async relative => {
   calls.push({ method: 'ListProjectLocal', relative })
   return relative === '.' ? [entry('src', 'src', true), entry('README.md', 'README.md'), entry('.env', '.env', false, !!settings.profile), entry('LICENSE.md', 'LICENSE.md')] : [entry('App.vue', relative + '/App.vue'), entry('utils', relative + '/utils', true)]
  },
	  GetAuthPrompt: async () => clone(activeAuthPrompt),
	  RespondAuthPrompt: async (id, answer, cancelled, remember) => {
	   authResponses.push({ id, answer, cancelled, remember })
	   if (id !== activeAuthPrompt?.id || !pendingAuthResolve) throw Error('Stale auth prompt response')
	   activeAuthPrompt = null
	   const resolve = pendingAuthResolve, reject = pendingAuthReject
	   pendingAuthResolve = null; pendingAuthReject = null
	   if (cancelled) { navigationState = 'disconnected'; navigationKey = ''; reject(Error('Authentication cancelled')) } else resolve()
	  },
	  ForgetSavedPassword: async (projectID, connectionID) => { forgetPasswordCalls.push({ projectID, connectionID }) },
	  ListRemote: async relative => {
	   calls.push({ method: 'ListRemote', relative }); navigationState = 'connected'; navigationKey = currentNavigationKey()
	   if (armedAuthPrompt) {
	    activeAuthPrompt = armedAuthPrompt; armedAuthPrompt = null
	    await new Promise((resolve, reject) => { pendingAuthResolve = resolve; pendingAuthReject = reject })
	   }
	   const project = settings.projects.find(p => p.id === settings.activeProjectID)
	   const connection = project?.connections.find(c => c.id === settings.activeConnectionID)
	   if (connection?.rootPath === '/var/www/html') {
	    if (relative === '.') return [entry('app', 'app', true), entry('unmapped', 'unmapped', true)]
	    if (relative === 'app') return [entry('index.php', 'app/index.php')]
	    return []
	   }
	   return [entry('server-file.txt', relative === '.' ? 'server-file.txt' : relative + '/server-file.txt'), ...(relative === '.' ? [entry('nested', 'nested', true)] : [])]
	  },
	  GetNavigationStatus: async () => navigationStatus(),
	  DisconnectRemote: async () => { calls.push({ method: 'DisconnectRemote' }); navigationState = 'disconnected'; navigationKey = '' },
  DiscoverSSH: async configPath => ({ configPath, aliases: [], warnings: [] }),
  EvaluateSSH: async () => ({ hostname: 'fixture', user: 'demo', port: 22, identityFiles: [], proxyJump: '', hasProxyCommand: false, configPath: '', alias: 'fixture' }),
  CancelOperation: async () => {},
	  GetQueue: async () => clone(queue),
	  SetQueuePaused: async paused => { queue.paused = paused; startNext() },
	  ClearFinishedQueue: async () => { queue.jobs = queue.jobs.filter(job => job.status === 'waiting' || job.status === 'running') },
	  CancelQueueJob: async id => { const job = queue.jobs.find(job => job.id === id); job.status = 'cancelled'; job.processedItems = job.items.length; for (const item of job.items) item.status = 'cancelled'; startNext() },
	  EnqueueBatch: async (token, policy, includeEnvironment) => {
	   calls.push({ method: 'EnqueueBatch', token, policy, includeEnvironment })
	   if (!lastPreview || lastPreview.token !== token) throw Error('Preview token already used')
	   const plan = lastPreview; lastPreview = undefined
	   queue.jobs.push({ id: 'job-' + (++queueNextID), projectName: settings.projectName, direction: plan.direction, server: plan.server, status: 'waiting', totalBytes: plan.totalBytes, transferredBytes: 0, processedItems: 0, error: '', items: plan.items.map(item => ({ ...item, status: 'waiting', transferredBytes: 0, error: '' })) })
	   startNext(); return clone(queue)
	  },
	  PreviewQueueRetry: async id => {
	   calls.push({ method: 'PreviewQueueRetry', id })
	   const job = queue.jobs.find(job => job.id === id)
	   const items = job.items.filter(item => item.status === 'failed' || item.status === 'cancelled').map(({status, transferredBytes, error, ...item}) => item)
	   lastPreview = { token: 'retry-token', direction: job.direction, server: job.server, items, fileCount: items.length, totalBytes: items.reduce((sum, item) => sum + item.size, 0) }
	   return clone(lastPreview)
	  },
  PreviewBatchForServer: async (projectID, connectionID, mappingID, direction, relatives) => {
   calls.push({ method: 'PreviewBatchForServer', projectID, connectionID, mappingID, direction, relatives: clone(relatives) })
   const profile = profileFor(projectID, connectionID, mappingID)
   lastPreview = { token: 'fixture-token', direction, server: profile.connection.alias || profile.connection.host, fileCount: relatives.length, totalBytes: relatives.length * 128,
    items: relatives.map(relativePath => ({ relativePath, source: direction === 'upload' ? profile.localRoot + '\\' + relativePath : profile.remoteRoot + '/' + relativePath, destination: direction === 'upload' ? profile.remoteRoot + '/' + relativePath : profile.localRoot + '\\' + relativePath, kind: 'file', size: 128, exists: false, skippedReason: '' })) }
	   return clone(lastPreview)
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
 await input('auth-mode', 'password')
 await click('default-server-select')
 await click('settings-save')
 await until(() => evaluate('window.__uiMock.settings.projects[0].connections.length === 2'), 'two servers saved')
 assert.equal(await evaluate(`window.__uiMock.settings.projects[0].connections[1].connection.authMode`), 'password', 'password auth mode persists in connection settings')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'SaveProject').at(-1).draft.connections.some(c => Object.hasOwn(c.connection, 'password') || Object.hasOwn(c.connection, 'passphrase'))`), false, 'SaveProject config contains no password or passphrase field')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'SaveProject').at(-1).draft.connections.some(c => ['remember', 'rememberPassword', 'savePassword'].some(key => Object.hasOwn(c.connection, key)))`), false, 'SaveProject config contains no remember preference')
 assert.equal(await evaluate('window.__uiMock.settings.projects[0].defaultConnectionID === window.__uiMock.settings.projects[0].connections[1].id'), true, 'user selected default')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 0)
 const productionID = await evaluate('window.__uiMock.settings.projects[0].connections[1].id')
 const developmentID = await evaluate('window.__uiMock.settings.projects[0].connections[0].id')
 await input('server-select', productionID)
 await until(() => evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('/srv/production')`), 'server root switched')
 await evaluate(`window.__uiMock.armAuthPrompt('old-password-prompt', 'Old password challenge')`)
 await click('connect-server')
 await until(() => evaluate(`window.__uiMock.authPending && document.querySelector('[data-testid="connect-server"]').disabled`), 'remote read waits for password prompt')
 await until(() => visible('auth-dialog'), 'password prompt dialog')
 assert.equal(await evaluate(`document.querySelector('[data-testid="auth-prompt"]').textContent`), 'Old password challenge')
 await input('auth-answer', 'stale answer must be cleared')
 await evaluate(`window.__uiMock.replaceAuthPrompt('current-password-prompt', 'Current password challenge')`)
 await until(() => evaluate(`document.querySelector('[data-testid="auth-prompt"]')?.textContent === 'Current password challenge' && document.querySelector('[data-testid="auth-answer"]')?.value === ''`), 'new password prompt clears prior answer')
 await until(() => visible('auth-remember'), 'remember-password checkbox for eligible prompt')
 assert.equal(await evaluate(`document.querySelector('[data-testid="auth-remember"]').checked`), false, 'remember checkbox defaults off')
 const unicodePassword = `пароль\"; $\`&雪 🔐`
 await input('auth-answer', unicodePassword)
 await check('auth-remember', true)
 await click('auth-submit')
 await until(() => evaluate(`window.__uiMock.authResponses.some(r => r.id === 'current-password-prompt')`), 'password answer submitted')
 assert.deepEqual(await evaluate(`window.__uiMock.authResponses`), [{ id: 'current-password-prompt', answer: unicodePassword, cancelled: false, remember: true }], 'exact Unicode password and remember choice reach only the auth response API')
 await until(() => evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('server-file.txt')`), 'remote file tree')
 assert.equal(await evaluate(`JSON.stringify(window.__uiMock.settings).includes(${JSON.stringify(unicodePassword)})`), false, 'password is absent from saved settings')
 assert.equal(await evaluate(`JSON.stringify(window.__uiMock.calls.filter(c => c.method === 'SaveProject').map(c => c.draft)).includes(${JSON.stringify(unicodePassword)})`), false, 'password is absent from every saved project config')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), 1, 'one explicit remote read')
 const layout = await evaluate(`(() => {
  const left = document.querySelector('[data-testid="local-pane"]').getBoundingClientRect()
  const right = document.querySelector('[data-testid="remote-pane"]').getBoundingClientRect()
  return { left: { x: left.x, width: left.width, height: left.height }, right: { x: right.x, width: right.width, height: right.height }, bodyWidth: document.body.scrollWidth, viewport: innerWidth }
 })()`)
 assert.ok(layout.left.x < layout.right.x && layout.left.width < layout.right.width && layout.left.height > 500 && layout.right.height > 500, 'IDE sidebar and two full-height panels')
 assert.ok(layout.bodyWidth <= layout.viewport, 'no horizontal page overflow')
 await screenshot('04-two-trees')
 assert.equal(await evaluate(`document.querySelectorAll('.tree-select, [data-testid="local-pane"] input[type="checkbox"], [data-testid="remote-pane"] input[type="checkbox"]').length`), 0, 'tree selection has no checkboxes')
 await clickTreeName('local', 'README.md')
 assert.deepEqual(await selectedTreeNames('local'), ['README.md'], 'plain click replaces selection')
 await clickTreeName('local', 'LICENSE.md', { ctrlKey: true })
 assert.deepEqual(new Set(await selectedTreeNames('local')), new Set(['README.md', 'LICENSE.md']), 'Ctrl-click adds to selection')
 await clickTreeName('local', 'src', { shiftKey: true })
 assert.deepEqual(new Set(await selectedTreeNames('local')), new Set(['src', 'README.md']), 'Shift-click selects the visible range')
 const localSrcReads = await evaluate(`window.__uiMock.calls.filter(call => call.method === 'ListProjectLocal' && call.relative === 'src').length`)
 await clickTreeName('local', 'src')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'ListProjectLocal' && call.relative === 'src').length`), localSrcReads, 'folder name click selects without expanding or reading')
 await evaluate(`(() => { const row = [...document.querySelectorAll('[data-testid="local-pane"] .tree-row')].find(row => row.querySelector('.tree-name')?.textContent.includes('src/')); row.querySelector('.tree-toggle').click() })()`)
 await until(() => evaluate(`window.__uiMock.calls.some(call => call.method === 'ListProjectLocal' && call.relative === 'src')`), 'local folder arrow read')
 await until(() => evaluate(`!![...document.querySelectorAll('[data-testid="local-pane"] .tree-row')].find(row => row.querySelector('.tree-name')?.textContent.includes('src/'))?.querySelector('.tree-toggle')`), 'local nested folder rendered')
 await clickTreeName('local', 'README.md')
 await clickTreeName('local', 'LICENSE.md', { ctrlKey: true })
 await contextTreeName('local', 'README.md')
 assert.deepEqual(new Set(await selectedTreeNames('local')), new Set(['README.md', 'LICENSE.md']), 'right click preserves a multi-selection')
 await screenshot('10-tree-context-menu')
 await click('menu-upload')
 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'batch preview modal')
 assert.ok(await evaluate(`document.querySelector('dialog.preview-modal').textContent.includes('/srv/production/README.md') && document.querySelector('dialog.preview-modal').textContent.includes('/srv/production/LICENSE.md')`), 'current server upload preview includes selected files')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).connectionID`), productionID, 'menu upload uses active server snapshot')
 assert.deepEqual(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).relatives`), ['README.md', 'LICENSE.md'])
 await screenshot('06-transfer-preview')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'Escape closes preview without empty modal')
 const projectSelectionsBeforeSnapshot = await evaluate(`window.__uiMock.calls.filter(call => call.method === 'SelectProject').length`)
 await clickTreeName('local', 'README.md')
 await contextTreeName('local', 'README.md')
 await click('menu-upload-servers')
 await until(() => evaluate(`!!document.querySelector('[data-testid="transfer-menu"] [data-server-id]')`), 'upload server submenu')
 await evaluate(`(() => { const item = document.querySelector('[data-testid="transfer-menu"] [data-server-id="${developmentID}"][data-direction="upload"]'); if (!item) throw Error('Development upload item missing'); item.click() })()`)
 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'other-server upload preview')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).connectionID`), developmentID, 'submenu upload uses chosen server snapshot')
 assert.ok(await evaluate(`document.querySelector('dialog.preview-modal').textContent.includes('/srv/demo/README.md')`), 'chosen server snapshot supplies its remote root')
 assert.equal(await evaluate('window.__uiMock.settings.activeConnectionID'), productionID, 'previewing another server leaves active server unchanged')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close alternate-server preview')
 await clickTreeName('remote', 'server-file.txt')
 await contextTreeName('remote', 'server-file.txt')
 await click('menu-download')
 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'current-server download preview')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).direction`), 'download')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).connectionID`), productionID, 'menu download uses active server snapshot')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close current-server download preview')
 await contextTreeName('remote', 'server-file.txt')
 await click('menu-download-servers')
 await until(() => evaluate(`!!document.querySelector('[data-testid="transfer-menu"] [data-server-id]')`), 'download server submenu')
 await evaluate(`(() => { const item = document.querySelector('[data-testid="transfer-menu"] [data-server-id="${developmentID}"][data-direction="download"]'); if (!item) throw Error('Development download item missing'); item.click() })()`)
 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'other-server download preview')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).connectionID`), developmentID, 'submenu download uses chosen server snapshot')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).direction`), 'download')
 assert.equal(await evaluate('window.__uiMock.settings.activeConnectionID'), productionID, 'download snapshot leaves active server unchanged')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close alternate-server download preview')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'SelectProject').length`), projectSelectionsBeforeSnapshot, 'server snapshot previews never select the project')
 await clickTreeName('local', 'README.md')
 await evaluate(`document.querySelector('[data-testid="local-pane"] .tree-toolbar button.primary').click()`)
 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'quick upload preview')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).connectionID`), productionID, 'quick upload uses default active server')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close quick upload preview')
 await clickTreeName('remote', 'server-file.txt')
 await evaluate(`document.querySelector('[data-testid="remote-pane"] .tree-toolbar button.primary').click()`)
 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'quick download preview')
 assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).connectionID`), productionID, 'quick download uses default active server')
 await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close quick download preview')
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
	// Queue UI uses an isolated deterministic mock; Go tests cover real SSH,
	// token/snapshot validation, cancellation, byte accounting and retry paths.
	await click('queue-toggle')
	await until(() => visible('queue-panel'), 'open queue')
	await click('queue-pause')
	await until(() => evaluate('window.__uiMock.queue.paused'), 'pause queue')
	async function enqueueReadme() {
	 await clickTreeName('local', 'README.md')
	 await until(() => evaluate(`!document.querySelector('[data-testid="local-pane"] .tree-toolbar button.primary').disabled`), 'queue selection ready')
	 await evaluate(`document.querySelector('[data-testid="local-pane"] .tree-toolbar button.primary').click()`)
	 await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'queue preview')
	 await click('queue-enqueue')
	 await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'enqueued preview closed')
	}
	await enqueueReadme()
	await enqueueReadme()
	assert.equal(await evaluate('window.__uiMock.queue.jobs.length'), 2)
	assert.equal(await evaluate(`window.__uiMock.queue.jobs.every(job => job.status === 'waiting')`), true)
	await until(() => evaluate(`document.querySelectorAll('[data-queue-id]').length === 2`), 'two queued jobs shown')
	await evaluate(`document.querySelectorAll('[data-queue-id]')[1].querySelector('[data-testid="queue-cancel"]').click()`)
	await until(() => evaluate(`window.__uiMock.queue.jobs[1].status === 'cancelled'`), 'cancel waiting job')
	await click('queue-pause')
	await until(() => evaluate(`window.__uiMock.queue.jobs[0].status === 'running'`), 'resume job')
	await until(() => evaluate(`document.querySelector('[data-queue-id]')?.textContent.includes('Передаётся')`), 'running status shown')
	assert.equal(await evaluate(`document.querySelector('[data-testid="settings-open"]').disabled`), false, 'settings usable during transfer')
	await evaluate(`window.__uiMock.progressJob('job-1', 64)`)
	await until(() => evaluate(`document.querySelector('[data-queue-id="job-1"] [role="progressbar"]').getAttribute('aria-valuenow') === '50'`), 'byte progress shown while running')
	await screenshot('08-transfer-queue')
	assert.ok(await evaluate('document.body.scrollWidth <= innerWidth'), 'queue at minimum width has no page overflow')
	await evaluate(`window.__uiMock.finishJob('job-1', 'failed')`)
	await until(() => evaluate(`!!document.querySelector('[data-testid="queue-retry"]')`), 'retry failed job')
	await input('server-select', developmentID)
	await until(() => evaluate(`window.__uiMock.settings.activeConnectionID === ${JSON.stringify(developmentID)}`), 'switch active server before retry')
	await click('queue-retry')
	await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'fresh retry preview')
	assert.ok(await evaluate(`document.querySelector('dialog.preview-modal').textContent.includes('/srv/production/README.md')`), 'retry keeps original destination')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewQueueRetry').length`), 1)
	await click('queue-enqueue')
	await until(() => evaluate('window.__uiMock.queue.jobs.length === 3'), 'retry queued')
	assert.equal(await evaluate(`window.__uiMock.queue.jobs[2].status`), 'waiting', 'retry remains paused until resume')
	await click('queue-pause')
	await until(() => evaluate(`window.__uiMock.queue.jobs[2].status === 'running'`), 'retry running')
	await evaluate(`window.__uiMock.finishJob('job-3')`)
	await until(() => evaluate(`document.querySelector('[data-queue-id="job-3"]')?.textContent.includes('Готово')`), 'completion status shown')
	await click('queue-clear')
	await until(() => evaluate('window.__uiMock.queue.jobs.length === 0'), 'clear finished jobs')
	// Quick actions keep the configured default even while browsing another server.
	await clickTreeName('local', 'README.md')
	await contextTreeName('local', 'README.md')
	await click('menu-upload')
	await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'default-server upload while browsing development')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'PreviewBatchForServer').at(-1).connectionID`), productionID, 'quick action retains configured default')
	assert.equal(await evaluate('window.__uiMock.settings.activeConnectionID'), developmentID, 'default quick action retains browsed server')
	await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close default-server preview')
	// A cached tree and a live navigation session are separate UI states.
	await click('connect-server')
	await until(() => evaluate(`document.querySelector('[data-testid="navigation-status"]').textContent.includes('Подключён')`), 'connected navigation status')
	const disconnectsBefore = await evaluate(`window.__uiMock.calls.filter(call => call.method === 'DisconnectRemote').length`)
	const remoteReadsBeforeFolder = await evaluate(`window.__uiMock.calls.filter(call => call.method === 'ListRemote').length`)
	await clickTreeName('remote', 'nested')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'ListRemote').length`), remoteReadsBeforeFolder, 'remote folder name click selects without expanding or reading')
	assert.equal(await evaluate(`[...document.querySelectorAll('[data-testid="remote-pane"] .tree-row')].find(row => row.querySelector('.tree-name')?.textContent.includes('nested/'))?.getAttribute('aria-expanded')`), 'false', 'remote folder name click leaves it collapsed')
	await evaluate(`([...document.querySelectorAll('[data-testid="remote-pane"] .tree-row')].find(row => row.querySelector('.tree-name')?.textContent.includes('nested/')).querySelector('.tree-toggle')).click()`)
	await until(() => evaluate(`window.__uiMock.calls.some(call => call.method === 'ListRemote' && call.relative === 'nested')`), 'nested folder read')
	await until(() => evaluate(`!document.querySelector('[data-testid="connect-server"]').disabled`), 'nested read finished')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'DisconnectRemote').length`), disconnectsBefore, 'folder navigation never disconnects')
	await click('connect-server')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'DisconnectRemote').length`), disconnectsBefore, 'ordinary refresh retains navigation')
	await screenshot('09-fast-navigation')
	assert.ok(await evaluate('document.body.scrollWidth <= innerWidth'), 'navigation controls at minimum width do not overflow')
	await click('disconnect-server')
	await until(() => evaluate(`window.__uiMock.navigation.state === 'disconnected' && !document.querySelector('[data-testid="remote-pane"]').textContent.includes('server-file.txt')`), 'disconnect clears remote tree')
	const remoteReadsBeforeReconnect = await evaluate(`window.__uiMock.calls.filter(call => call.method === 'ListRemote').length`)
	await click('reconnect-server')
	await until(() => evaluate(`window.__uiMock.navigation.state === 'connected' && document.querySelector('[data-testid="remote-pane"]').textContent.includes('server-file.txt')`), 'explicit reconnect loads tree')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'ListRemote').length`), remoteReadsBeforeReconnect + 1, 'reconnect lists once')
	await evaluate('window.__uiMock.dropNavigation()')
	await until(() => evaluate(`document.querySelector('[data-testid="navigation-status"]').textContent.includes('Отключён')`), 'idle/drop status updates')
	assert.ok(await evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('server-file.txt')`), 'disconnected session can retain cached tree')
	await input('server-select', productionID)
	await until(() => evaluate(`window.__uiMock.settings.activeConnectionID === ${JSON.stringify(productionID)}`), 'switch server resets navigation without automatic SSH')
	assert.equal(await evaluate('window.__uiMock.navigation.state'), 'disconnected')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(call => call.method === 'ListRemote').length`), remoteReadsBeforeReconnect + 1, 'switch server never auto connects')
	// Server browsing root is independent of the local and deployment mapping.
	await click('settings-open')
	await input('remote-root', '/var/www/html')
	await input('mapping-local-path', 'src')
	await input('deployment-path', 'app')
	await evaluate(`document.querySelector('[data-testid="remote-root"]').scrollIntoView({ block: 'center' })`)
	await screenshot('11-root-deployment-settings')
	await click('settings-save')
	await until(() => evaluate(`window.__uiMock.settings.profile.remoteRoot === '/var/www/html/app'`), 'computed deployment destination')
	const rootReadsBefore = await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`)
	await click('connect-server')
	await until(() => evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('unmapped/')`), 'server root lists mapped and unmapped folders')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').at(-1).relative`), '.', 'root listing stays server-root-relative')
	assert.ok(await evaluate(`document.querySelector('[data-testid="remote-pane"] .root-path').textContent.includes('/var/www/html')`))
	assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'ListRemote').length`), rootReadsBefore + 1)
	await evaluate(`([...document.querySelectorAll('[data-testid="local-pane"] .tree-row')].find(row => row.querySelector('.tree-name')?.textContent.includes('src/')).querySelector('.tree-toggle')).click()`)
	await until(() => evaluate(`document.querySelector('[data-testid="local-pane"]').textContent.includes('App.vue') && !document.querySelector('[data-testid="connect-server"]').disabled`), 'local mapped folder loaded')
	await clickTreeName('local', 'App.vue')
	await evaluate(`document.querySelector('[data-testid="local-pane"] .tree-toolbar button.primary').click()`)
	await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'mapped upload preview')
	assert.ok(await evaluate(`document.querySelector('dialog.preview-modal').textContent.includes('/var/www/html/app/App.vue')`), 'upload joins root and deployment path')
	assert.deepEqual(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'PreviewBatchForServer').at(-1).relatives`), ['App.vue'])
	await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close mapped upload preview')
	await evaluate(`([...document.querySelectorAll('[data-testid="remote-pane"] .tree-row')].find(row => row.querySelector('.tree-name')?.textContent.includes('app/')).querySelector('.tree-toggle')).click()`)
	await until(() => evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('index.php') && !document.querySelector('[data-testid="connect-server"]').disabled`), 'deployment folder loaded')
	await clickTreeName('remote', 'index.php')
	await contextTreeName('remote', 'index.php')
	await click('menu-download')
	await until(() => evaluate(`!!document.querySelector('dialog.preview-modal[open]')`), 'mapped download preview')
	assert.ok(await evaluate(`document.querySelector('dialog.preview-modal').textContent.includes('/var/www/html/app/index.php') && document.querySelector('dialog.preview-modal').textContent.includes('src\\\\index.php')`), 'download uses deployment source and mapped local folder')
	assert.deepEqual(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'PreviewBatchForServer').at(-1).relatives`), ['index.php'])
	await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await until(() => evaluate(`!document.querySelector('dialog[open]')`), 'close mapped download preview')
	await screenshot('12-root-deployment-tree')
	await evaluate(`([...document.querySelectorAll('[data-testid="remote-pane"] .tree-row')].find(row => row.querySelector('.tree-name')?.textContent.includes('unmapped/')).querySelector('.tree-toggle')).click()`)
	await until(() => evaluate(`!document.querySelector('[data-testid="connect-server"]').disabled`), 'unmapped folder can be browsed')
	await clickTreeName('remote', 'unmapped')
	const previewsBeforeUnmapped = await evaluate(`window.__uiMock.calls.filter(c => c.method === 'PreviewBatchForServer').length`)
	await contextTreeName('remote', 'unmapped')
	await click('menu-download')
	await until(() => evaluate(`!!document.querySelector('.toast[role="alert"]')`), 'unmapped transfer shows validation error')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'PreviewBatchForServer').length`), previewsBeforeUnmapped, 'unmapped path never starts preview')
	assert.equal(await evaluate(`!!document.querySelector('dialog[open]')`), false)
	const disconnectsBeforeMappingEdit = await evaluate(`window.__uiMock.calls.filter(c => c.method === 'DisconnectRemote').length`)
	await click('settings-open')
	await input('deployment-path', 'another')
	await click('settings-save')
	await until(() => evaluate(`window.__uiMock.settings.profile.remoteRoot === '/var/www/html/another'`), 'deployment edit changes transfer profile')
	await click('connect-server')
	await until(() => evaluate(`document.querySelector('[data-testid="remote-pane"]').textContent.includes('app/')`), 'deployment edit leaves browsing at same server root')
	assert.equal(await evaluate(`window.__uiMock.settings.projects[0].connections.find(c => c.id === window.__uiMock.settings.activeConnectionID).rootPath`), '/var/www/html')
	assert.equal(await evaluate(`window.__uiMock.calls.filter(c => c.method === 'DisconnectRemote').length`), disconnectsBeforeMappingEdit, 'deployment edit does not disconnect navigation')
	await click('disconnect-server')
	await until(() => evaluate(`window.__uiMock.navigation.state === 'disconnected'`), 'disconnect before auth cancellation checks')
	await evaluate(`window.__uiMock.armAuthPrompt('cancel-password-prompt', 'Cancel password challenge', true)`)
	await click('reconnect-server')
	await until(() => evaluate(`window.__uiMock.authPending && document.querySelector('[data-testid="connect-server"]').disabled`), 'remote read waits for cancellable prompt')
	await until(() => visible('auth-dialog'), 'cancellable password dialog')
	await until(() => visible('auth-remember'), 'remember option on cancellable prompt')
	await input('auth-answer', 'must never be returned')
	await check('auth-remember', true)
	await click('auth-cancel')
	await until(() => evaluate(`window.__uiMock.authResponses.some(r => r.id === 'cancel-password-prompt') && !document.querySelector('dialog[data-testid="auth-dialog"][open]')`), 'password prompt cancelled')
	assert.deepEqual(await evaluate(`window.__uiMock.authResponses.at(-1)`), { id: 'cancel-password-prompt', answer: '', cancelled: true, remember: false }, 'cancel returns no password and never remembers')
	await until(() => evaluate(`!document.querySelector('[data-testid="connect-server"]').disabled && window.__uiMock.navigation.state === 'disconnected'`), 'cancelled connect settles')
	await evaluate(`window.__uiMock.armAuthPrompt('escape-password-prompt', 'Escape password challenge', false)`)
	await click('reconnect-server')
	await until(() => evaluate(`window.__uiMock.authPending && document.querySelector('[data-testid="connect-server"]').disabled`), 'remote read waits for Escape prompt')
	await until(() => visible('auth-dialog'), 'Escape-cancellable password dialog')
	assert.equal(await visible('auth-remember'), false, 'prompt that cannot remember has no remember checkbox')
	await input('auth-answer', 'also must never be returned')
	await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
	await until(() => evaluate(`window.__uiMock.authResponses.some(r => r.id === 'escape-password-prompt') && !document.querySelector('dialog[data-testid="auth-dialog"][open]')`), 'Escape cancels password prompt')
	assert.deepEqual(await evaluate(`window.__uiMock.authResponses.at(-1)`), { id: 'escape-password-prompt', answer: '', cancelled: true, remember: false }, 'Escape cancellation returns no password and never remembers')
	await until(() => evaluate(`!document.querySelector('[data-testid="connect-server"]').disabled && window.__uiMock.navigation.state === 'disconnected'`), 'Escape-cancelled connect settles')
	console.log('Browser checks passed: settings, password prompt submit/reset/cancel, selection/context menus, queue, persistent navigation, independent root/deployment paths, mapped uploads/downloads and unmapped browsing. Screenshots:', output)
} catch (error) {
 if (socket?.readyState === WebSocket.OPEN) {
  try {
   await screenshot('failure')
   console.error('UI failure state:', await evaluate(`({ dialogs: [...document.querySelectorAll('dialog[open]')].map(e => e.textContent.slice(0, 1500)), alerts: [...document.querySelectorAll('[role="alert"]')].map(e => e.textContent), calls: window.__uiMock.calls.slice(-8), queue: window.__uiMock.queue, servers: window.__uiMock.settings.projects.map(p => p.connections.map(c => c.name)) })`))
  } catch {}
 }
 throw error
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
