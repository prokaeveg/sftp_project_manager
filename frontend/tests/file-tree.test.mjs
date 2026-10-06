import test from 'node:test'
import assert from 'node:assert/strict'
import { treeRoot, treeNode, toggleDirectory, loadDirectory, selectEntry, toMappingPath } from '../src/file-tree.ts'

const folder = (name, relativePath = name) => ({ name, relativePath, size: 0, isDir: true, isLink: false, excluded: false })
const file = (name, relativePath = name) => ({ ...folder(name, relativePath), isDir: false, size: 12 })

test('multiple selections persist across lazy collapse and use full relative paths', async () => {
 const root = treeRoot(), selection = new Set()
 await loadDirectory(root, async () => [folder('src'), file('other')])
 await loadDirectory(root.children[0], async () => [file('same.ts', 'src/same.ts')])
 selectEntry(selection, root.children[0].entry, true)
 selectEntry(selection, root.children[0].children[0].entry, true)
 selectEntry(selection, file('same.ts', 'another/same.ts'), true)
 await toggleDirectory(root.children[0], async () => { throw new Error('collapse must not connect') })
 assert.deepEqual([...selection], ['src', 'src/same.ts', 'another/same.ts'])
 selectEntry(selection, root.children[0].entry, false)
 assert.deepEqual([...selection], ['src/same.ts', 'another/same.ts'])
 selectEntry(selection, root.entry, true)
 assert.equal(selection.has('.'), true)
})

test('excluded items and links cannot enter a selection; panels are independent', () => {
 const local = new Set(), remote = new Set()
 selectEntry(local, file('ok'), true)
 selectEntry(local, { ...folder('.git'), excluded: true }, true)
 selectEntry(local, { ...folder('junction'), isLink: true }, true)
 selectEntry(remote, file('ok'), true)
 selectEntry(local, file('ok'), false)
 assert.deepEqual([...local], [])
 assert.deepEqual([...remote], ['ok'])
})

test('loads only expanded directories and reuses cached branches', async () => {
 const root = treeRoot(), calls = []
 const loader = async path => {
  calls.push(path)
  return path === '.' ? [folder('src'), folder('other')] : [file('main.ts', 'src/main.ts')]
 }
 assert.deepEqual(calls, [])
 await toggleDirectory(root, loader)
 assert.deepEqual(calls, ['.'])
 const src = root.children[0]
 await toggleDirectory(src, loader)
 assert.deepEqual(calls, ['.', 'src'])
 assert.equal(src.children[0].entry.relativePath, 'src/main.ts')
 assert.equal(root.children[1].loaded, false)
 await toggleDirectory(src, loader)
 assert.equal(src.expanded, false)
 await toggleDirectory(src, loader)
 assert.equal(src.expanded, true)
 assert.deepEqual(calls, ['.', 'src'])
})

test('refresh removes disappeared entries and invalidates descendant caches', async () => {
 const root = treeRoot()
 await loadDirectory(root, async () => [folder('src'), file('deleted')])
 await loadDirectory(root.children[0], async () => [file('old', 'src/old')])
 await loadDirectory(root, async () => [folder('src'), file('new')])
 assert.deepEqual(root.children.map(n => n.entry.name), ['src', 'new'])
 assert.equal(root.children[0].loaded, false)
 assert.equal(root.children[0].expanded, false)
 assert.deepEqual(root.children[0].children, [])
})

test('failed refresh preserves previous contents, clears loading, and can retry', async () => {
 const root = treeRoot()
 await loadDirectory(root, async () => [file('previous')])
 await assert.rejects(loadDirectory(root, async () => { throw new Error('cancelled') }), /cancelled/)
 assert.equal(root.loading, false)
 assert.match(root.error, /cancelled/)
 assert.equal(root.children[0].entry.name, 'previous')
 await toggleDirectory(root, async () => { throw new Error('must not load on collapse') })
 await toggleDirectory(root, async () => [])
 assert.equal(root.error, '')
 assert.equal(root.loaded, true)
 assert.deepEqual(root.children, [])
})

test('first load failure is retryable; empty/null listings are loaded once', async () => {
 const root = treeRoot()
 await assert.rejects(toggleDirectory(root, async () => { throw new Error('offline') }), /offline/)
 assert.equal(root.loaded, false)
 assert.equal(root.loading, false)
 await loadDirectory(root, async () => null)
 await toggleDirectory(root, async () => { throw new Error('cached') })
 await toggleDirectory(root, async () => { throw new Error('cached') })
 assert.equal(root.loaded, true)
 assert.deepEqual(root.children, [])
})

test('excluded paths, links and files cannot trigger a directory request', async () => {
 for (const entry of [{ ...folder('.git'), excluded: true }, { ...folder('link'), isLink: true }, file('text')]) {
  const node = treeNode(entry)
  const loader = async () => { throw new Error('must not connect') }
  await toggleDirectory(node, loader)
  await loadDirectory(node, loader)
  assert.equal(node.expanded, false)
  assert.equal(node.loaded, false)
 }
})

test('duplicate loading is suppressed and an old root cannot populate a new selection', async () => {
 const oldRoot = treeRoot(), newRoot = treeRoot()
 let finish, calls = 0
 const loader = () => { calls++; return new Promise(resolve => { finish = resolve }) }
 const pending = loadDirectory(oldRoot, loader)
 await loadDirectory(oldRoot, loader)
 assert.equal(calls, 1)
 assert.equal(oldRoot.loading, true)
 finish([folder('old-project')])
 await pending
 assert.deepEqual(newRoot.children, [])
 assert.equal(newRoot.loaded, false)
})

test('upload paths are mapped relative to the selected local mapping with boundary checks', () => {
 assert.equal(toMappingPath('.', '.'), '.')
 assert.equal(toMappingPath('src', 'src'), '.')
 assert.equal(toMappingPath('src/components/App.vue', 'src'), 'components/App.vue')
 assert.throws(() => toMappingPath('src-old/file', 'src'), /вне маппинга/)
 assert.throws(() => toMappingPath('.', 'src'), /вне маппинга/)
 assert.throws(() => toMappingPath('../secret', '.'), /выходит за пределы проекта/)
 assert.throws(() => toMappingPath('C:/outside/file', '.'), /не относится к папке проекта/)
})
