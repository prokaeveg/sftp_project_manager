import test from 'node:test'
import assert from 'node:assert/strict'
import { gitChangesTree, gitStatusMap, gitStatusForPath } from '../src/git-status.ts'
import { selectTreeEntry } from '../src/file-tree.ts'

const files = [
 {path:'ignored.txt',status:'ignored'}, {path:'cache',status:'ignored',isDir:true}, {path:'clean.txt', status:'clean'}, {path:'src/changed.ts',status:'modified'},
 {path:'new.txt',status:'added'}, {path:'src/untracked.ts',status:'untracked'},
 {path:'src/deleted.ts',status:'deleted'}, {path:'other/conflict.ts',status:'conflict'}
]
test('Git colors include clean files and aggregate folder changes', () => {
 const statuses = gitStatusMap(files)
 assert.equal(statuses['clean.txt'],'clean')
 assert.equal(statuses['src/changed.ts'],'modified')
 assert.equal(statuses['src'],'deleted')
 assert.equal(statuses['other'],'conflict')
 assert.equal(statuses['absent'],undefined)
 assert.equal(gitStatusMap([{path:'__proto__/file',status:'added'}])['__proto__'],'added')
})
test('changes list and tree preserve paths and omit unchanged and untracked files', () => {
 const list = gitChangesTree(files,'list')
 assert.equal(list.children.length,4)
 assert.ok(list.children.every(node=>!node.entry.isDir))
 assert.ok(list.children.some(node=>node.entry.name==='src/changed.ts'))
 const tree = gitChangesTree(files,'tree')
 const src = tree.children.find(node=>node.entry.relativePath==='src')
 assert.equal(src.children.length,2)
 assert.equal(src.children.find(node=>node.entry.name==='changed.ts').entry.relativePath,'src/changed.ts')
 assert.equal(src.children.find(node=>node.entry.name==='deleted.ts').entry.missing,true)
 assert.equal(tree.children.some(node=>node.entry.relativePath==='clean.txt'),false)
 assert.equal(src.children.some(node=>node.entry.relativePath==='src/untracked.ts'),false)
})
test('range selection skips deleted files', () => {
 const root = gitChangesTree(files,'list')
 const missing = root.children.find(node=>node.entry.missing).entry
 const selection = new Set()
 selectTreeEntry(selection,root,missing,{shiftKey:false,ctrlKey:false,metaKey:false})
 assert.equal(selection.size,0)
 const last = root.children.at(-1).entry
 selectTreeEntry(selection,root,last,{shiftKey:true,ctrlKey:false,metaKey:false},root.children[0].entry.relativePath)
 assert.equal(selection.has(missing.relativePath),false)
})

test('unversioned group contains only untracked paths in both views', () => {
 const list = gitChangesTree(files,'list','unversioned')
 assert.deepEqual(list.children.map(node=>node.entry.relativePath), ['src/untracked.ts'])
 const tree = gitChangesTree(files,'tree','unversioned')
 assert.equal(tree.children[0].entry.relativePath,'src')
 assert.equal(tree.children[0].children[0].entry.relativePath,'src/untracked.ts')
 assert.equal(gitChangesTree([{path:'clean',status:'clean'}],'tree','unversioned').children.length,0)
})

test('ignored folders color descendants without coloring their parents or overriding tracked files', () => {
 const statuses = gitStatusMap([{path:'src/cache',status:'ignored',isDir:true},{path:'src/cache/tracked.txt',status:'clean'}])
 assert.equal(statuses['src'],'clean')
 assert.equal(gitStatusForPath(statuses,'src/cache/deep/file.txt'),'ignored')
 assert.equal(gitStatusForPath(statuses,'src/cache/tracked.txt'),'clean')
 assert.equal(gitStatusForPath(statuses,'src/cache-other/file.txt'),undefined)
 assert.equal(gitChangesTree([{path:'cache',status:'ignored'}],'list').children.length,0)
 assert.equal(gitChangesTree([{path:'cache',status:'ignored'}],'list','unversioned').children.length,0)
})
