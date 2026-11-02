import test from 'node:test'
import assert from 'node:assert/strict'
import { recentBranches, rememberBranch } from '../src/git-recent.ts'

test('recent branches are bounded, deduplicated and isolated by repository', () => {
 const values = new Map()
 globalThis.localStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }
 for (let i = 0; i < 7; i++) rememberBranch('repo-a', `refs/heads/${i}`)
 rememberBranch('repo-a', 'refs/heads/4')
 assert.deepEqual(recentBranches('repo-a'), ['refs/heads/4', 'refs/heads/6', 'refs/heads/5', 'refs/heads/3', 'refs/heads/2'])
 assert.deepEqual(recentBranches('repo-b'), [])
 values.set('git-compare:repo-a', '{invalid')
 assert.deepEqual(recentBranches('repo-a'), [])
 globalThis.localStorage = { getItem() { throw Error('disabled') }, setItem() { throw Error('disabled') } }
 assert.doesNotThrow(() => rememberBranch('repo-a', 'refs/heads/main'))
 assert.deepEqual(recentBranches('repo-a'), [])
 delete globalThis.localStorage
})
