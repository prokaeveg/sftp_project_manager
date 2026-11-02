import test from 'node:test'
import assert from 'node:assert/strict'
import { submenuPosition } from '../src/submenu-position.ts'

test('side menus open right, flip left at the edge and stay inside the viewport', () => {
 assert.deepEqual(submenuPosition({ left: 100, right: 374, top: 120 }, 240, 200, 1200, 800), { left: 374, top: 120 })
 assert.deepEqual(submenuPosition({ left: 900, right: 1174, top: 700 }, 240, 200, 1200, 800), { left: 660, top: 592 })
 assert.deepEqual(submenuPosition({ left: 8, right: 282, top: 10 }, 270, 300, 300, 200), { left: 8, top: 8 })
})
