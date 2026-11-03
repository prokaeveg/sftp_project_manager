import test from 'node:test'
import assert from 'node:assert/strict'
import { createBackend } from '../src/bridge-adapter.ts'

test('bridge normalizes nullable Go collections and optional selection without losing settings', async () => {
 const raw = { GetStatus: async () => ({ platform: 'windows/amd64', settings: { schemaVersion: 3, projectName: '', projects: [{ id: 'p', connections: null }] } }) }
 const status = await createBackend(raw).GetStatus()
 assert.equal(status.platform, 'windows/amd64')
 assert.equal(status.settings.schemaVersion, 3)
 assert.deepEqual(status.settings.projects[0].connections, [])
 assert.equal(status.settings.activeProjectID, '')
})

test('bridge forwards auth cancellation arguments and preserves null prompts', async () => {
 const calls = []
 const api = createBackend({ GetAuthPrompt: async () => null, RespondAuthPrompt: async (...args) => { calls.push(args) } })
 assert.equal(await api.GetAuthPrompt(), null)
 await api.RespondAuthPrompt('prompt-2', '', true, false)
 assert.deepEqual(calls, [['prompt-2', '', true, false]])
})

test('bridge keeps legacy connection modes readable without changing saved configuration', async () => {
 const raw = { GetStatus: async () => ({ settings: { projects: [{ id: 'p', connections: [{ id: 'c', connection: { mode: '' }, mappings: null }] }] } }) }
 const result = await createBackend(raw).GetStatus()
 assert.equal(result.settings.projects[0].connections[0].connection.mode, '')
})

test('bridge propagates backend failures and does not turn them into empty results', async () => {
 const failure = new Error('Соединение закрыто')
 const api = createBackend({ ListRemote: async () => { throw failure } })
 await assert.rejects(api.ListRemote('src'), error => error === failure)
})

test('bridge handles empty listings and checks status values at the typed boundary', async () => {
 const api = createBackend({ ListProjectLocal: async () => null, GetNavigationStatus: async () => ({ state: 'connected', projectID: 'p', connectionID: 'c' }), GetGitStatus: async () => ({ available: true, files: [{ path: 'a', status: 'unexpected' }] }) })
 assert.deepEqual(await api.ListProjectLocal(''), [])
 assert.deepEqual(await api.GetNavigationStatus(), { state: 'connected', projectID: 'p', connectionID: 'c' })
 await assert.rejects(api.GetGitStatus('p'), /unexpected/)
})

test('project opening preserves the source window status and passes the requested mode', async () => {
 const calls = []
 const api = createBackend({ OpenProject: async (...args) => { calls.push(args); return { action: 'new', status: { settings: { activeProjectID: 'source', projects: [] } } } } })
 const result = await api.OpenProject('target', 'new')
 assert.deepEqual(calls, [['target', 'new']])
 assert.equal(result.action, 'new')
 assert.equal(result.status.settings.activeProjectID, 'source')
 assert.equal(result.status.settings.activeConnectionID, '')
})
