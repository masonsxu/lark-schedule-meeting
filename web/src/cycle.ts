import type { Rule } from './types'

/** 与后端 model.OccursOn 一致的循环推算（浏览器本地时区口径）。 */

function parseDate(s: string): Date {
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y, m - 1, d)
}

function isSameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  )
}

/** addMonths 与 Go time.AddDate 同语义：目标月没有该日时自动顺延（1月31日 + 1月 = 3月3日）。 */
function addMonths(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth() + n, d.getDate())
}

/** occursOnBase 判断 date 是否为一次目标日期（不考虑周末跳过）。 */
function occursOnBase(rule: Rule, date: Date): boolean {
  const start = parseDate(rule.startDate)
  if (isNaN(start.getTime()) || date < start) return false
  const cycle = rule.cycle || 'once'
  if (cycle === 'once') return isSameDay(start, date)
  if (cycle === 'daily') return true
  if (cycle === 'weekly') {
    const diff = Math.round((date.getTime() - start.getTime()) / 86400000)
    return diff % 7 === 0
  }
  const step = cycle === 'quarterly' ? 3 : 1
  for (let m = 0; ; m += step) {
    const d = addMonths(start, m)
    if (d > date) return false
    if (isSameDay(d, date)) return true
  }
}

/** occursOn 判断 date 是否为有效目标日期（含周末跳过，默认跳过）。 */
export function occursOn(rule: Rule, date: Date): boolean {
  if (!occursOnBase(rule, date)) return false
  if (rule.skipWeekends !== false) {
    const wd = date.getDay()
    if (wd === 0 || wd === 6) return false
  }
  return true
}

export function leadDaysOf(rule: Rule): number {
  return rule.leadDays && rule.leadDays > 0 ? rule.leadDays : 30
}

/** nextGrab 计算下一次抢座计划：返回抢座日与目标会议日期。 */
export function nextGrab(
  rule: Rule,
  from: Date = new Date(),
): { grabDate: string; targetDate: string } | null {
  const lead = leadDaysOf(rule)
  const fmt = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  const today = new Date(from.getFullYear(), from.getMonth(), from.getDate())
  for (let i = 0; i < 400; i++) {
    const gd = new Date(today.getFullYear(), today.getMonth(), today.getDate() + i)
    const target = new Date(today.getFullYear(), today.getMonth(), today.getDate() + i + lead)
    if (occursOn(rule, target)) {
      return { grabDate: fmt(gd), targetDate: fmt(target) }
    }
  }
  return null
}
