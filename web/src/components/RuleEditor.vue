<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { api } from '../api'
import type { Rule, RoomRef } from '../types'
import { CYCLE_OPTIONS } from '../types'
import RoomPanel from './RoomPanel.vue'

const props = defineProps<{ rule: Rule | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

interface DraftRule {
  name: string
  description: string
  startDate: string
  cycle: string
  rooms: RoomRef[]
  startTime: string
  endTime: string
  triggerTime: string
  leadDays: number
  skipWeekends: boolean
  enabled: boolean
  allowNoRoom: boolean
}

function defaultStartDate(): string {
  const d = new Date(Date.now() + 30 * 86400000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function emptyDraft(): DraftRule {
  return {
    name: '',
    description: '',
    startDate: defaultStartDate(),
    cycle: 'once',
    rooms: [],
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
const firstInput = ref<HTMLInputElement | null>(null)

onMounted(() => {
  if (props.rule) {
    draft.value = {
      name: props.rule.name,
      description: props.rule.description ?? '',
      startDate: props.rule.startDate,
      cycle: props.rule.cycle || 'once',
      rooms: JSON.parse(JSON.stringify(props.rule.rooms ?? [])),
      startTime: props.rule.startTime,
      endTime: props.rule.endTime,
      triggerTime: props.rule.triggerTime,
      leadDays: props.rule.leadDays && props.rule.leadDays > 0 ? props.rule.leadDays : 30,
      skipWeekends: props.rule.skipWeekends !== false,
      enabled: props.rule.enabled,
      allowNoRoom: props.rule.allowNoRoom ?? false,
    }
  }
  nextTick(() => firstInput.value?.focus())
  window.addEventListener('keydown', onKeydown)
})

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

function addRoom(room: { id: string; name: string }) {
  if (draft.value.rooms.some((r) => r.id === room.id)) return
  draft.value.rooms.push({ id: room.id, name: room.name ?? '' })
}

function moveRoom(index: number, delta: -1 | 1) {
  const list = draft.value.rooms
  const j = index + delta
  if (j < 0 || j >= list.length) return
  ;[list[index], list[j]] = [list[j], list[index]]
}

function removeRoom(index: number) {
  draft.value.rooms.splice(index, 1)
}

function validate(): string[] {
  const errs: string[] = []
  if (!draft.value.name.trim()) errs.push('会议主题不能为空')
  if (!draft.value.startDate) errs.push('首次目标日期不能为空')
  if (draft.value.rooms.length === 0) errs.push('至少配置一间会议室（固定 ID）')
  for (const room of draft.value.rooms) {
    if (!room.id.trim()) errs.push(`会议室 ${room.name || '（未命名）'} 缺少固定 ID`)
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
              <label for="rule-start-date">首次目标日期</label>
              <input id="rule-start-date" v-model="draft.startDate" type="date" required />
            </div>
            <div class="field">
              <label for="rule-cycle">循环规则</label>
              <select id="rule-cycle" v-model="draft.cycle">
                <option v-for="opt in CYCLE_OPTIONS" :key="opt.value" :value="opt.value">
                  {{ opt.label }}
                </option>
              </select>
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
              <label for="rule-desc">备注（可选）</label>
              <input id="rule-desc" v-model="draft.description" type="text" placeholder="可选" />
            </div>
          </div>
          <p class="hint">
            目标日期从首次目标日期起按循环规则依次推进（每天 / 每周同星期 / 每月同日 / 每季度同日）；
            抢座日 = 目标日期 - 提前天数（默认 30：公司会议室最早可提前 30 天预约），到达"抢座开始时刻"后开抢：
            10 分钟窗口内每分钟一次，按优先级以固定会议室 ID 直传预约。不循环 = 只抢一次，抢完自动关闭规则。
          </p>
        </fieldset>

        <fieldset class="form-section">
          <legend>会议室优先级（固定 ID，依次尝试）</legend>
          <div v-if="draft.rooms.length > 0" class="rooms">
            <span v-for="(room, i) in draft.rooms" :key="room.id" class="room-chip">
              <span class="room-priority mono">{{ i + 1 }}</span>
              <span class="room-chip-name" :title="`${room.name || ''} ${room.id}`">
                {{ room.name || room.id }}
              </span>
              <span class="room-chip-id mono">{{ room.id }}</span>
              <button type="button" class="chip-btn" :disabled="i === 0" aria-label="上移" @click="moveRoom(i, -1)">↑</button>
              <button type="button" class="chip-btn" :disabled="i === draft.rooms.length - 1" aria-label="下移" @click="moveRoom(i, 1)">↓</button>
              <button type="button" class="chip-btn chip-remove" aria-label="移除" @click="removeRoom(i)">×</button>
            </span>
          </div>
          <p v-else class="hint">还没有会议室。在下方名录中录入后点"加入"，或手动录入已知 ID。</p>
          <RoomPanel
            :default-start="draft.startTime"
            :default-end="draft.endTime"
            @add="addRoom"
          />
        </fieldset>

        <fieldset class="form-section">
          <legend>行为选项</legend>
          <label class="check-row">
            <input v-model="draft.skipWeekends" type="checkbox" />
            <span>目标日期落在周六/周日时跳过（默认开启）</span>
          </label>
          <label class="check-row">
            <input v-model="draft.allowNoRoom" type="checkbox" />
            <span>会议室全部预约失败时仍创建无房会议</span>
          </label>
          <label class="check-row">
            <input v-model="draft.enabled" type="checkbox" />
            <span>启用规则（停用后调度器不再触发）</span>
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
  line-height: 1.6;
}

.rooms {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 12px;
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
