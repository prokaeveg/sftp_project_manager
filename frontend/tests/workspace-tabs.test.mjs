import test from 'node:test'
import assert from 'node:assert/strict'
import { comparisonKey, putComparisonTab } from '../src/workspace-tabs.ts'

test('comparisons coexist with files and distinguish projects, paths, Git refs and SSH servers', () => {
 const tabs = [{ key: 'file', kind: 'file', file: { name: 'a.txt' } }]
 const first = { local: { name: 'a.txt', text: 'one' }, remote: { text: 'two' } }
 const keys = [
  comparisonKey('p', 'a.txt', 'git', 'refs/heads/main'),
  comparisonKey('p', 'a.txt', 'git', 'refs/heads/dev'),
  comparisonKey('p', 'a.txt', 'ssh', 'main'),
  comparisonKey('p', 'a.txt', 'ssh', 'other'),
  comparisonKey('q', 'a.txt', 'git', 'refs/heads/main'),
  comparisonKey('p', 'b.txt', 'git', 'refs/heads/main'),
 ]
 assert.equal(new Set(keys).size, keys.length)
 for (const key of keys) putComparisonTab(tabs, key, first)
 assert.equal(tabs.length, 7)
 const refreshed = { ...first, local: { name: 'a.txt', text: 'updated' } }
 putComparisonTab(tabs, keys[0], refreshed)
 assert.equal(tabs.length, 7)
 assert.equal(tabs[1].comparison.local.text, 'updated')
 assert.equal(tabs[2].comparison.local.text, 'one')
 assert.equal(tabs[0].kind, 'file')
})
