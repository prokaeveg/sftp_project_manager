import test from 'node:test'
import assert from 'node:assert/strict'
import { eligibleConnections, eligibleCopyConnections, matchingMapping } from '../src/mapping-availability.ts'

const server = (id, paths) => ({ id, mappings: paths.map((localPath, i) => ({ id: `${id}-${i}`, localPath, deploymentPath: '.' })) })
const connections = [server('orel', ['orel-site']), server('tambov', ['tambov-site']), server('both', ['orel-site', 'tambov-site']), server('root', ['.']), server('none', [])]
test('only servers with a mapping covering the selection are offered', () => {
 assert.deepEqual(eligibleConnections(connections, ['orel-site/index.php']).map(c => c.id), ['orel', 'both', 'root'])
 assert.deepEqual(eligibleConnections(connections, ['tambov-site/index.php']).map(c => c.id), ['tambov', 'both', 'root'])
 assert.deepEqual(eligibleConnections(connections, ['orel-site']).map(c => c.id), ['orel', 'both', 'root'])
 assert.deepEqual(eligibleConnections(connections, ['orel-site-other/index.php']).map(c => c.id), ['root'])
 assert.deepEqual(eligibleConnections(connections, ['orel-site/a', 'orel-site/b']).map(c => c.id), ['orel', 'both', 'root'])
 assert.deepEqual(eligibleConnections(connections, ['orel-site/a', 'tambov-site/b']).map(c => c.id), ['root'])
 assert.deepEqual(eligibleConnections(connections, []), [])
 assert.deepEqual(eligibleConnections(connections, ['../outside']), [])
})
test('uses the most specific common mapping and handles Windows separators', () => {
 const connection = server('nested', ['.', 'orel-site', 'orel-site/src'])
 assert.equal(matchingMapping(connection, ['orel-site\\src\\a.php']).localPath, 'orel-site/src')
 assert.equal(matchingMapping(connection, ['orel-site/src/a.php', 'orel-site/assets/b.css']).localPath, 'orel-site')
})

test('copy targets require a mapping for every path, allowing separate mappings', () => {
 assert.deepEqual(eligibleCopyConnections(connections, ['orel-site/a', 'tambov-site/b']).map(c => c.id), ['both', 'root'])
 assert.deepEqual(eligibleCopyConnections(connections, ['orel-site/a']).map(c => c.id), ['orel', 'both', 'root'])
 assert.deepEqual(eligibleCopyConnections(connections, []), [])
})

test('unmapped servers serialized as null by Go do not hide mapped servers', () => {
 const snapshot = [server('torel.interrao.ru', ['interrao-orel.ru']), { id: 'unmapped', mappings: null }]
 assert.deepEqual(eligibleConnections(snapshot, ['interrao-orel.ru/index.php']).map(c => c.id), ['torel.interrao.ru'])
 assert.deepEqual(eligibleCopyConnections(snapshot, ['interrao-orel.ru/index.php']).map(c => c.id), ['torel.interrao.ru'])
 assert.equal(matchingMapping({ mappings: null }, ['interrao-orel.ru/index.php']), undefined)
})
