/** 与后端 server/internal/model 对应的领域类型。 */

export interface RoomRef {
  id: string
  name: string
}

export interface Rule {
  id: string
  name: string
  description?: string
  /** 键 "1"=周一 ... "7"=周日，值为按优先级排序的会议室列表 */
  weekdays: Record<string, RoomRef[]>
  startTime: string
  endTime: string
  triggerTime: string
  /** 提前多少天的“抢座日”开始抢目标日期；缺省 30（公司最早可提前 30 天预约） */
  leadDays?: number
  /** 目标日期落在周六/周日时跳过；缺省 true */
  skipWeekends?: boolean
  enabled: boolean
  allowNoRoom?: boolean
  windowMinutes?: number
  intervalSeconds?: number
  createdAt: string
  updatedAt: string
}

export type RecordStatus = 'booked' | 'occupied' | 'failed'

export interface BookingRecord {
  id: string
  ruleId: string
  ruleName: string
  date: string
  start: string
  end: string
  roomId?: string
  roomName?: string
  status: RecordStatus
  source: 'auto' | 'manual' | 'detected'
  eventId?: string
  note?: string
  attempts: number
  createdAt: string
  updatedAt: string
}

export interface Session {
  ruleId: string
  ruleName: string
  date: string
  source: string
  startedAt: string
  deadline: string
  nextAttemptAt: string
  attemptsDone: number
  maxAttempts: number
  status: 'running' | 'booked' | 'occupied' | 'failed'
  lastMessage?: string
}

export interface Attempt {
  id: string
  at: string
  ruleId: string
  ruleName: string
  date: string
  kind: 'session-start' | 'find' | 'create' | 'skip' | 'session-end'
  ok: boolean
  detail: string
}

export interface Room {
  id: string
  name: string
  capacity?: number
  source?: string
  updatedAt?: string
}

export interface AppConfig {
  port: number
  dataDir: string
  larkCliPath: string
  timezone: string
  tickSeconds: number
  grabWindowMinutes: number
  grabIntervalSeconds: number
  attemptLogCap: number
}

export const WEEKDAY_KEYS = ['1', '2', '3', '4', '5', '6', '7'] as const

export const WEEKDAY_NAMES: Record<string, string> = {
  '1': '周一',
  '2': '周二',
  '3': '周三',
  '4': '周四',
  '5': '周五',
  '6': '周六',
  '7': '周日',
}
