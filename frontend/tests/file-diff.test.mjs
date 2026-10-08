import test from 'node:test'
import assert from 'node:assert/strict'
import { compareLines } from '../src/file-diff.ts'

test('diff aligns insertions, deletions, replacements and preserves each side', () => {
 for (const [left, right] of [['a\nb\nc', 'a\nnew\nb\nc'], ['a\nold\nb', 'a\nb'], ['a\nx\nc', 'a\ny\nc'], ['', 'x\n'], ['a\na\nb', 'a\nb\nb']]) {
  const { rows } = compareLines(left, right)
  assert.deepEqual(rows.filter(r => r.left !== undefined).map(r => r.left), left.split('\n'))
  assert.deepEqual(rows.filter(r => r.right !== undefined).map(r => r.right), right.split('\n'))
  assert.deepEqual(rows.filter(r => r.left !== undefined).map(r => r.leftNumber), left.split('\n').map((_, i) => i + 1))
  assert.deepEqual(rows.filter(r => r.right !== undefined).map(r => r.rightNumber), right.split('\n').map((_, i) => i + 1))
 }
 const rows = compareLines('a\nb\nc', 'a\nnew\nb\nc').rows
 assert.equal(rows[1].left, undefined); assert.equal(rows[1].right, 'new'); assert.equal(rows[2].changed, false)
})

test('diff normalizes CRLF only for presentation and bounds huge changes', () => {
 assert.equal(compareLines('a\r\nb\r\n', 'a\nb\n').rows.some(r => r.changed), false)
 const left = Array.from({ length: 1500 }, (_, i) => 'left' + i).join('\n'), right = left.replaceAll('left', 'right')
 const result = compareLines(left, right)
 assert.equal(result.simplified, true); assert.equal(result.rows.length, 1500)
 assert.equal(compareLines('\n'.repeat(10001), '\n'.repeat(10001)).limited, true)
 assert.equal(compareLines('\n'.repeat(10001), '\n'.repeat(10001)).rows.length, 10000)
})
