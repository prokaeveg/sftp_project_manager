import test from 'node:test'
import assert from 'node:assert/strict'
import { fitPanels, readPanelSizes } from '../src/panel-layout.ts'

test('panels retain independent widths and leave room for the editor', () => {
 assert.deepEqual(fitPanels(1000, { left: 240, right: 260 }), { left: 240, right: 260 })
 const fitted = fitPanels(676, { left: 600, right: 600 })
 assert.ok(fitted.left >= 175 && fitted.right >= 195)
 assert.ok(fitted.left + fitted.right <= 446)
 assert.deepEqual(fitPanels(1000, { left: -20, right: 260 }), { left: 175, right: 260 })
})

test('stored sizes tolerate missing, malformed and nonfinite values', () => {
 for (const raw of [null, '{', '{}', 'null', '{"left":-1,"right":300}', '{"left":1e999,"right":300}']) {
  assert.deepEqual(readPanelSizes(raw), { left: 260, right: 300 })
 }
 assert.deepEqual(readPanelSizes('{"left":210,"right":250}'), { left: 210, right: 250 })
})
