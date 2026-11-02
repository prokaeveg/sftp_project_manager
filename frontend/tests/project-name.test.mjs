import test from 'node:test'
import assert from 'node:assert/strict'
import { projectNameFromRoot } from '../src/project-name.ts'

test('suggests the root folder name only when the current name is blank', () => {
 const root = 'C:\\Users\\egor\\PhpstormProjects\\bitrix_marketplace'
 assert.equal(projectNameFromRoot('', root), 'bitrix_marketplace')
 assert.equal(projectNameFromRoot('   ', root + '\\'), 'bitrix_marketplace')
 assert.equal(projectNameFromRoot('', '/home/user/my-project/'), 'my-project')
 assert.equal(projectNameFromRoot('Custom name', root), 'Custom name')
 assert.equal(projectNameFromRoot(' Custom name ', root), ' Custom name ')
 for (const value of ['', '   ', 'C:\\', '/', '\\\\server\\share\\']) {
  assert.equal(projectNameFromRoot('', value), '')
 }
})
