import test from 'node:test'
import assert from 'node:assert/strict'
import { directorySearchExcluded } from '../src/search-exclusions.ts'

test('search marks cover subtrees with boundaries, dot defaults and explicit inclusion', () => {
 const project = { excludeDotDirectories: true, searchExcludedDirectories: ['Vendor'], searchIncludedDirectories: ['.idea'] }
 assert.equal(directorySearchExcluded(project, '.'), false)
 assert.equal(directorySearchExcluded(project, 'vendor/lib'), true)
 assert.equal(directorySearchExcluded(project, 'vendor-extra'), false)
 assert.equal(directorySearchExcluded(project, '.cache/nested'), true)
 assert.equal(directorySearchExcluded(project, '.idea/nested'), false)
 assert.equal(directorySearchExcluded(project, '.idea/.cache/nested'), true)
 assert.equal(directorySearchExcluded({}, '.cache'), false)
})
