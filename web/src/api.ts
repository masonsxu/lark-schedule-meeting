import type { AppConfig, Attempt, BookingRecord, Room, Rule, Session } from './types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    const message = (body as { error?: string }).error ?? `HTTP ${res.status}`
    throw new Error(message)
  }
  return body as T
}

export const api = {
  health: () =>
    request<{
      ok: boolean
      lark: { ok: boolean; error: string; identity: { userName: string; openId: string } }
      now: string
    }>('/api/health'),

  listRules: () => request<Rule[]>('/api/rules'),
  createRule: (rule: Omit<Rule, 'id' | 'createdAt' | 'updatedAt'>) =>
    request<Rule>('/api/rules', { method: 'POST', body: JSON.stringify(rule) }),
  updateRule: (id: string, rule: Omit<Rule, 'id' | 'createdAt' | 'updatedAt'>) =>
    request<Rule>(`/api/rules/${id}`, { method: 'PUT', body: JSON.stringify(rule) }),
  deleteRule: (id: string) => request<{ ok: boolean }>(`/api/rules/${id}`, { method: 'DELETE' }),

  runRule: (id: string, opts: { date?: string; maxAttempts?: number; intervalSeconds?: number; force?: boolean }) =>
    request<{ ok: boolean }>(`/api/rules/${id}/run`, { method: 'POST', body: JSON.stringify(opts) }),

  records: () => request<BookingRecord[]>('/api/records'),
  deleteRecord: (id: string, deleteEvent: boolean) =>
    request<{ ok: boolean }>(`/api/records/${id}?deleteEvent=${deleteEvent}`, { method: 'DELETE' }),

  sessions: () => request<Session[]>('/api/sessions'),
  attempts: (limit = 100) => request<Attempt[]>(`/api/attempts?limit=${limit}`),

  roomsAvailable: (start: string, end: string, roomNames?: string[]) =>
    request<{ start: string; end: string; rooms: Room[] }>('/api/rooms/available', {
      method: 'POST',
      body: JSON.stringify({ start, end, roomNames }),
    }),

  rooms: () => request<Room[]>('/api/rooms'),
  addRoom: (id: string, name: string) =>
    request<{ ok: boolean }>('/api/rooms', { method: 'POST', body: JSON.stringify({ id, name }) }),
  deleteRoom: (id: string) => request<{ ok: boolean }>(`/api/rooms/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  config: () => request<AppConfig>('/api/config'),
}
