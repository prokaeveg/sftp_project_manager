export interface DiffRow { left?: string; right?: string; leftNumber?: number; rightNumber?: number; changed: boolean }
export interface LineDiff { rows: DiffRow[]; simplified: boolean; limited: boolean }

// Bounded LCS on the changed middle keeps common insertions aligned without
// allowing adversarial/large files to allocate a quadratic matrix.
export function compareLines(leftText: string, rightText: string): LineDiff {
 const allLeft = leftText.replace(/\r\n/g, '\n').split('\n'), allRight = rightText.replace(/\r\n/g, '\n').split('\n')
 const limited = allLeft.length > 10000 || allRight.length > 10000
 const left = allLeft.slice(0, 10000), right = allRight.slice(0, 10000)
 const rows: DiffRow[] = []
 let prefix = 0, suffix = 0
 while (prefix < Math.min(left.length, right.length) && left[prefix] === right[prefix]) prefix++
 while (suffix < Math.min(left.length, right.length) - prefix && left[left.length - 1 - suffix] === right[right.length - 1 - suffix]) suffix++
 for (let i = 0; i < prefix; i++) rows.push({ left: left[i], right: right[i], leftNumber: i + 1, rightNumber: i + 1, changed: false })
 const a = left.slice(prefix, left.length - suffix), b = right.slice(prefix, right.length - suffix)
 const simplified = (a.length + 1) * (b.length + 1) > 1_000_000 || left.length + right.length > 20000
 let leftNumber = prefix + 1, rightNumber = prefix + 1
 function block(deleted: string[], inserted: string[]) {
  for (let k = 0; k < Math.max(deleted.length, inserted.length); k++) {
   const row: DiffRow = { changed: true }
   if (k < deleted.length) { row.left = deleted[k]; row.leftNumber = leftNumber++ }
   if (k < inserted.length) { row.right = inserted[k]; row.rightNumber = rightNumber++ }
   rows.push(row)
  }
 }
 if (simplified) block(a, b)
 else {
  const width = b.length + 1, table = new Uint32Array((a.length + 1) * width)
  for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--) table[i * width + j] = a[i] === b[j] ? table[(i + 1) * width + j + 1]! + 1 : Math.max(table[(i + 1) * width + j]!, table[i * width + j + 1]!)
  let i = 0, j = 0, deleted: string[] = [], inserted: string[] = []
  function flush() { block(deleted, inserted); deleted = []; inserted = [] }
  while (i < a.length || j < b.length) {
   if (i < a.length && j < b.length && a[i] === b[j]) {
    flush(); rows.push({ left: a[i], right: b[j], leftNumber: leftNumber++, rightNumber: rightNumber++, changed: false }); i++; j++
   } else if (i < a.length && (j === b.length || table[(i + 1) * width + j]! >= table[i * width + j + 1]!)) deleted.push(a[i++]!)
   else inserted.push(b[j++]!)
  }
  flush()
 }
 for (let i = 0; i < suffix; i++) rows.push({ left: left[left.length - suffix + i], right: right[right.length - suffix + i], leftNumber: leftNumber++, rightNumber: rightNumber++, changed: false })
 return { rows, simplified, limited }
}
