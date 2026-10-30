const units = ['\u0411', '\u041a\u0411', '\u041c\u0411', '\u0413\u0411', '\u0422\u0411', '\u041f\u0411', '\u042d\u0411']

export function formatBytes(value: number): string {
 if (!Number.isFinite(value) || value <= 0) return `0 ${units[0]}`
 let amount = value
 let unit = 0
 while (amount >= 1024 && unit < units.length - 1) {
  amount /= 1024
  unit++
 }
 return `${amount.toLocaleString('ru-RU', { maximumFractionDigits: unit === 0 ? 0 : 1 })} ${units[unit]}`
}
