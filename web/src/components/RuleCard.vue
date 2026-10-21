<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'
import type { BookingRecord, Rule } from '../types'
import { WEEKDAY_KEYS, WEEKDAY_NAMES } from '../types'

const props = defineProps<{ rule: Rule; records: BookingRecord[] }>()
const emit = defineEmits<{ edit: [rule: Rule]; changed: [] }>()

const busy = ref(false)
const error = ref('')

const activeWeekdays = computed(() =>
  WEEKDAY_KEYS.filter((k) => (props.rule.weekdays[k] ?? []).length > 0),
)

const latestRecord = computed(() =>
  [...props.records].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))[0],
)

/** 下一次抢座计划：抢座日 + 时刻 -> 目标会议日期。 */
function nextGrab(rule: Rule): { grabDate: string; targetDate: string } | null {
  const keys: string[] = WEEKDAY_KEYS.filter((k) => (rule.weekdays[k] ?? []).length > 0)
  if (keys.length === 0) return null
  const lead = rule.leadDays && rule.leadDays > 0 ? rule.leadDays : 30
  const skipWE = rule.skipWeekends !== false
  const fmt = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  const now = new Date()
  for (let i = 0; i < 75; i++) {
    const gd = new Date(now.getFullYear(), now.getMonth(), now.getDate() + i)
    const td = new Date(gd.getFullYear(), gd.getMonth(), gd.getDate() + lead)
    const iso = String(td.getDay() === 0 ? 7 : td.getDay())
    if (!keys.includes(iso)) continue
    if (skipWE && (iso === '6' || iso === '7')) continue
    return { grabDate: fmt(gd), targetDate: fmt(td) }
  }
  return null
}

async function toggleEnabled() {
  busy.value = true
  error.value = ''
  try {
    await api.updateRule(props.rule.id, { ...props.rule, enabled: !props.rule.enabled })
    emit('changed')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}

async function runNow() {
  busy.value = true
  error.value = ''
  try {
    const plan = nextGrab(props.rule)
    await api.runRule(props.rule.id, { date: plan?.targetDate })
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}

async function remove() {
  if (!window.confirm(`删除规则「${props.rule.name}」？已有预约记录与飞书日程不受影响。`)) return
  busy.value = true
  error.value = ''
  try {
    await api.deleteRule(props.rule.id)
    emit('changed')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <article class="panel rule-card" :class="{ disabled: !rule.enabled }">
    <div class="card-head">
      <h3 class="rule-name" :title="rule.name">{{ rule.name }}</h3>
      <label class="switch" :title="rule.enabled ? '已启用' : '已停用'">
        <input type="checkbox" :checked="rule.enabled" :disabled="busy" @change="toggleEnabled" />
        <span class="track" aria-hidden="true" />
        <span class="visually-hidden">{{ rule.enabled ? '停用规则' : '启用规则' }}</span>
      </label>
    </div>

    <dl class="rule-meta">
      <div class="meta-row">
        <dt>会议时间</dt>
        <dd class="mono">{{ rule.startTime }} – {{ rule.endTime }}</dd>
      </div>
      <div class="meta-row">
        <dt>抢座开始</dt>
        <dd class="mono">{{ rule.triggerTime }}（提前 {{ rule.leadDays && rule.leadDays > 0 ? rule.leadDays : 30 }} 天）</dd>
      </div>
      <div v-if="nextGrab(rule)" class="meta-row">
        <dt>下次抢座</dt>
        <dd class="mono">{{ nextGrab(rule)!.grabDate }} {{ rule.triggerTime }} → 目标 {{ nextGrab(rule)!.targetDate }}</dd>
      </div>
    </dl>

    <div class="weekday-chips">
      <span
        v-for="k in WEEKDAY_KEYS"
        :key="k"
        class="chip"
        :class="{ on: activeWeekdays.includes(k) }"
        :title="activeWeekdays.includes(k) ? rule.weekdays[k].map((r) => r.name || r.id).join(' → ') : ''"
      >
        {{ WEEKDAY_NAMES[k] }}
      </span>
    </div>

    <p v-if="latestRecord" class="latest">
      <span class="badge" :class="'badge-' + latestRecord.status">
        {{ latestRecord.status === 'booked' ? '已预约' : latestRecord.status === 'occupied' ? '无房' : '失败' }}
      </span>
      <span class="mono latest-date">{{ latestRecord.date }}</span>
      <span v-if="latestRecord.roomName" class="latest-room">{{ latestRecord.roomName }}</span>
    </p>

    <p v-if="error" class="card-error" role="alert">{{ error }}</p>

    <div class="card-actions">
      <button class="btn btn-sm" type="button" :disabled="busy" @click="runNow" :title="`立即抢目标日期 ${nextGrab(rule)?.targetDate ?? ''}`">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M13 2 4 14h6l-1 8 9-12h-6l1-8z" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" />
        </svg>
        立即抢
      </button>
      <button class="btn btn-sm" type="button" :disabled="busy" @click="emit('edit', rule)">编辑</button>
      <button class="btn btn-sm btn-ghost" type="button" :disabled="busy" @click="remove">删除</button>
    </div>
  </article>
</template>

<style scoped>
.rule-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
  transition: border-color 160ms ease;
}

.rule-card:hover {
  border-color: var(--border-strong);
}

.rule-card.disabled .rule-name {
  color: var(--text-muted);
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.rule-name {
  font-size: 16px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.rule-meta {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.meta-row {
  display: flex;
  gap: 10px;
  font-size: 13.5px;
}

.meta-row dt {
  color: var(--text-muted);
  width: 60px;
  flex: none;
}

.meta-row dd {
  margin: 0;
}

.weekday-chips {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.chip {
  padding: 2px 9px;
  border-radius: 999px;
  font-size: 12.5px;
  color: var(--text-muted);
  border: 1px solid var(--border);
  background: var(--surface-2);
}

.chip.on {
  color: var(--accent);
  border-color: rgba(76, 125, 255, 0.45);
  background: var(--accent-soft);
}

.latest {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text-muted);
}

.latest-date {
  font-size: 12.5px;
}

.latest-room {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.card-error {
  margin: 0;
  font-size: 13px;
  color: var(--danger);
}

.card-actions {
  margin-top: auto;
  display: flex;
  gap: 8px;
}

/* 开关 */
.switch {
  position: relative;
  display: inline-flex;
  cursor: pointer;
  flex: none;
}

.switch input {
  position: absolute;
  opacity: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  cursor: pointer;
}

.switch .track {
  width: 38px;
  height: 22px;
  border-radius: 999px;
  background: var(--surface-3);
  border: 1px solid var(--border-strong);
  transition: background-color 160ms ease, border-color 160ms ease;
  display: block;
}

.switch .track::after {
  content: '';
  display: block;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--text-muted);
  margin: 2px;
  transition: transform 160ms ease, background-color 160ms ease;
}

.switch input:checked + .track {
  background: var(--accent-strong);
  border-color: var(--accent-strong);
}

.switch input:checked + .track::after {
  transform: translateX(16px);
  background: #fff;
}

.switch input:focus-visible + .track {
  box-shadow: var(--focus-ring);
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}
</style>
