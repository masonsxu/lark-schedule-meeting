<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import type { Rule, RoomRef } from '../types'
import { WEEKDAY_KEYS, WEEKDAY_NAMES } from '../types'
import RoomPanel from './RoomPanel.vue'

const props = defineProps<{ rule: Rule | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

interface DraftRule {
  name: string
  description: string
  weekdays: Record<string, RoomRef[]>
  startTime: string
  endTime: string
  triggerTime: string
  leadDays: number
  skipWeekends: boolean
  enabled: boolean
  allowNoRoom: boolean
}

function emptyDraft(): DraftRule {
  return {
    name: '',
    description: '',
    weekdays: {},
    startTime: '10:00',
    endTime: '11:00',
    triggerTime: '09:00',
    leadDays: 30,
    skipWeekends: true,
    enabled: true,
    allowNoRoom: false,
  }
}

const draft = ref<DraftRule>(emptyDraft())
const saving = ref(false)
const errors = ref<string[]>([])
const dialogEl = ref<HTMLElement | null>(null)
const firstInput = ref<HTMLInputElement | null>(null)

// 目标星期：从会议室选择器加入房间时写入的星期。
const targetWeekday = ref('1')

onMounted(() => {
  if (props.rule) {
    draft.value = {
      name: props.rule.name,
      description: props.rule.description ?? '',
      weekdays: JSON.parse(JSON.stringify(props.rule.weekdays ?? {})),
      startTime: props.rule.startTime,
      endTime: props.rule.endTime,
      triggerTime: props.rule.triggerTime,
      leadDays: props.rule.leadDays && props.rule.leadDays > 0 ? props.rule.leadDays : 30,
      skipWeekends: props.rule.skipWeekends !== false,
      enabled: props.rule.enabled,
      allowNoRoom: props.rule.allowNoRoom ?? false,
    }
    const first = WEEKDAY_KEYS.find((k) => (draft.value.weekdays[k] ?? []).length > 0)
    if (first) targetWeekday.value = first
  }
  nextTick(() => firstInput.value?.focus())

  window.addEventListener('keydown', onKeydown)
})

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

/** 星期已启用（键存在，即使还没配会议室）。 */
function isDayOn(k: string): boolean {
  return draft.value.weekdays[k] !== undefined
}

/** 已配置会议室的星期数量。 */
const activeCount = computed(() => WEEKDAY_KEYS.filter((k) => (draft.value.weekdays[k] ?? []).length > 0).length)

watch(
  () => draft.value.weekdays,
  () => {
    if (!isDayOn(targetWeekday.value)) {
      const first = WEEKDAY_KEYS.find(isDayOn)
      if (first) targetWeekday.value = first
    }
  },
  { deep: true },
)

function toggleWeekday(k: string) {
  if (isDayOn(k)) {
    delete draft.value.weekdays[k]
  } else {
    draft.value.weekdays[k] = []
    targetWeekday.value = k
  }
}

function addRoom(room: { id: string; name: string }) {
  const list = draft.value.weekdays[targetWeekday.value] ?? (draft.value.weekdays[targetWeekday.value] = [])
  if (list.some((r) => r.id === room.id)) return
  list.push({ id: room.id, name: room.name ?? '' })
}

function moveRoom(k: string, index: number, delta: -1 | 1) {
  const list = draft.value.weekdays[k]
  if (!list) return
  const j = index + delta
  if (j < 0 || j >= list.length) return
  ;[list[index], list[j]] = [list[j], list[index]]
}

function removeRoom(k: string, index: number) {
  const list = draft.value.weekdays[k]
  if (!list) return
  list.splice(index, 1)
  if (list.length === 0) delete draft.value.weekdays[k]
}

function validate(): string[] {
  const errs: string[] = []
  if (!draft.value.name.trim()) errs.push('会议主题不能为空')
  if (activeCount.value === 0) errs.push('至少为一个星期配置会议室')
  for (const k of WEEKDAY_KEYS) {
    const rooms = draft.value.weekdays[k] ?? []
    for (const room of rooms) {
      if (!room.id.trim()) errs.push(`星期 ${WEEKDAY_NAMES[k]} 的会议室 ${room.name || '（未命名）'} 缺少 ID`)
    }
  }
  if (draft.value.endTime <= draft.value.startTime) errs.push('会议结束时间必须晚于开始时间')
  if (!(draft.value.leadDays >= 0 && draft.value.leadDays <= 365)) errs.push('提前天数应在 0-365 之间')
  return errs
}

async function save() {
  errors.value = validate()
  if (errors.value.length > 0) return
  saving.value = true
  try {
    const payload = { ...draft.value, name: draft.value.name.trim() }
    if (props.rule) {
      await api.updateRule(props.rule.id, payload)
    } else {
      await api.createRule(payload)
    }
    emit('saved')
  } catch (err) {
    errors.value = [err instanceof Error ? err.message : String(err)]
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <div
      ref="dialogEl"
      class="modal panel"
      role="dialog"
      aria-modal="true"
      :aria-label="props.rule ? '编辑规则' : '新建规则'"
    >
      <header class="modal-head">
        <h2>{{ props.rule ? '编辑规则' : '新建规则' }}</h2>
        <button class="btn btn-ghost btn-sm" type="button" aria-label="关闭" @click="emit('close')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M6 6l12 12M18 6 6 18" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" />
          </svg>
        </button>
      </header>

      <div v-if="errors.length > 0" class="error-summary" role="alert">
        <p v-for="e in errors" :key="e">{{ e }}</p>
      </div>

      <form class="modal-body" @submit.prevent="save">
        <fieldset class="form-section">
          <legend>基本信息</legend>
          <div class="form-grid">
            <div class="field span-2">
              <label for="rule-name">会议主题</label>
              <input
                id="rule-name"
                ref="firstInput"
                v-model="draft.name"
                type="text"
                required
                placeholder="如 站会 / 周会"
              />
            </div>
            <div class="field">
              <label for="rule-start">会议开始</label>
              <input id="rule-start" v-model="draft.startTime" type="time" required />
            </div>
            <div class="field">
              <label for="rule-end">会议结束</label>
              <input id="rule-end" v-model="draft.endTime" type="time" required />
            </div>
            <div class="field">
              <label for="rule-trigger">抢座开始时刻</label>
              <input id="rule-trigger" v-model="draft.triggerTime" type="time" required />
            </div>
            <div class="field">
              <label for="rule-lead">提前天数</label>
              <input id="rule-lead" v-model.number="draft.leadDays" type="number" min="0" max="365" required />
            </div>
            <div class="field span-2">
              <label for="rule-desc">备注（写入日程描述，可选）</label>
              <input id="rule-desc" v-model="draft.description" type="text" placeholder="可选" />
            </div>
          </div>
          <p class="hint">
            目标日期 = 抢座日 + 提前天数（默认 30：公司规则为会议室最早可提前 30 天预约）。
            抢座日到达"抢座开始时刻"后开抢：10 分钟窗口内每分钟一次，按优先级以固定会议室 ID 直传预约；
            失败记入 SQLite 记录，后续循环自动跳过已处理日期。
          </p>
        </fieldset>

        <fieldset class="form-section">
          <legend>星期与会议室优先级</legend>
          <ul class="weekday-list">
            <li v-for="k in WEEKDAY_KEYS" :key="k" class="weekday-row" :class="{ active: isDayOn(k) }">
              <label class="weekday-toggle">
                <input type="checkbox" :checked="isDayOn(k)" @change="toggleWeekday(k)" />
                <span>{{ WEEKDAY_NAMES[k] }}</span>
              </label>
              <div v-if="isDayOn(k)" class="rooms">
                <span v-for="(room, i) in draft.weekdays[k]" :key="room.id" class="room-chip">
                  <span class="room-priority mono">{{ i + 1 }}</span>
                  <span class="room-chip-name" :title="`${room.name || ''} ${room.id}`">
                    {{ room.name || room.id }}
                  </span>
                  <span class="room-chip-id mono">{{ room.id }}</span>
                  <button type="button" class="chip-btn" :disabled="i === 0" aria-label="上移" @click="moveRoom(k, i, -1)">↑</button>
                  <button type="button" class="chip-btn" :disabled="i === draft.weekdays[k].length - 1" aria-label="下移" @click="moveRoom(k, i, 1)">↓</button>
                  <button type="button" class="chip-btn chip-remove" aria-label="移除" @click="removeRoom(k, i)">×</button>
                </span>
              </div>
              <span v-else class="weekday-off-hint">不触发</span>
              <span v-if="isDayOn(k) && (draft.weekdays[k] ?? []).length === 0" class="weekday-off-hint">勾选后从下方加入会议室</span>
            </li>
          </ul>

          <div class="picker-target">
            <label for="target-weekday">将选择的会议室加入</label>
            <select id="target-weekday" v-model="targetWeekday">
              <option v-for="k in WEEKDAY_KEYS.filter(isDayOn)" :key="k" :value="k">{{ WEEKDAY_NAMES[k] }}</option>
              <option v-if="activeCount === 0" value="" disabled>请先勾选星期</option>
            </select>
          </div>
          <RoomPanel
            v-if="WEEKDAY_KEYS.some(isDayOn)"
            :default-start="draft.startTime"
            :default-end="draft.endTime"
            @add="addRoom"
          />
        </fieldset>

        <fieldset class="form-section">
          <legend>行为选项</legend>
          <label class="check-row">
            <input v-model="draft.enabled" type="checkbox" />
            <span>启用规则（停用后调度器不再触发）</span>
          </label>
          <label class="check-row">
            <input v-model="draft.skipWeekends" type="checkbox" />
            <span>目标日期落在周六/周日时跳过（默认开启）</span>
          </label>
          <label class="check-row">
            <input v-model="draft.allowNoRoom" type="checkbox" />
            <span>会议室全部预约失败时仍创建无房会议</span>
          </label>
        </fieldset>
      </form>

      <footer class="modal-foot">
        <button class="btn" type="button" @click="emit('close')">取消</button>
        <button class="btn btn-primary" type="button" :disabled="saving" @click="save">
          {{ saving ? '保存中…' : '保存规则' }}
        </button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  background: rgba(5, 8, 14, 0.72);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 4vh 16px 16px;
  overflow-y: auto;
  z-index: 40;
}

.modal {
  width: min(760px, 100%);
  max-height: 92vh;
  display: flex;
  flex-direction: column;
}

.modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}

.modal-head h2 {
  font-size: 17px;
}

.modal-body {
  padding: 6px 20px;
  overflow-y: auto;
}

.modal-foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--border);
}

.error-summary {
  margin: 12px 20px 0;
  padding: 10px 14px;
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 13.5px;
}

.error-summary p {
  margin: 2px 0;
}

.form-section {
  border: none;
  margin: 0;
  padding: 16px 0 6px;
}

.form-section legend {
  font-size: 13px;
  color: var(--text-muted);
  padding-bottom: 10px;
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.span-2 {
  grid-column: span 2;
}

.hint {
  margin: 10px 0 0;
  color: var(--text-muted);
  font-size: 12.5px;
}

.weekday-list {
  list-style: none;
  margin: 0 0 14px;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.weekday-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid transparent;
}

.weekday-row.active {
  background: var(--surface-2);
  border-color: var(--border);
}

.weekday-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 52px;
  cursor: pointer;
  padding-top: 3px;
}

.weekday-toggle input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent-strong);
}

.weekday-off-hint {
  color: var(--text-muted);
  font-size: 13px;
  padding-top: 4px;
}

.rooms {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  flex: 1;
}

.room-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 6px 3px 4px;
  border-radius: var(--radius-sm);
  background: var(--accent-soft);
  border: 1px solid rgba(76, 125, 255, 0.4);
  font-size: 13px;
}

.room-priority {
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--accent-strong);
  color: #fff;
  font-size: 11px;
}

.room-chip-name {
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.room-chip-id {
  font-size: 11px;
  color: var(--accent);
  opacity: 0.85;
  max-width: 130px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chip-btn {
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0 3px;
  font-size: 13px;
  line-height: 1;
}

.chip-btn:hover:not(:disabled) {
  color: var(--text);
}

.chip-btn:disabled {
  opacity: 0.3;
  cursor: default;
}

.chip-remove:hover {
  color: var(--danger);
}

.picker-target {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  font-size: 13.5px;
}

.picker-target select {
  width: auto;
  min-height: 34px;
}

.check-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 0;
  cursor: pointer;
}

.check-row input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent-strong);
}

@media (max-width: 560px) {
  .form-grid {
    grid-template-columns: 1fr;
  }
  .span-2 {
    grid-column: span 1;
  }
}
</style>
