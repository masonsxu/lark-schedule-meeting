<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api'
import type { Room } from '../types'

const emit = defineEmits<{ add: [room: { id: string; name: string }] }>()

const props = defineProps<{
  /** 用于查询可用性的默认时间（HH:MM）。 */
  defaultStart?: string
  defaultEnd?: string
}>()

const tomorrow = (() => {
  const d = new Date(Date.now() + 24 * 3600 * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})()

const date = ref(tomorrow)
const start = ref(props.defaultStart ?? '10:00')
const end = ref(props.defaultEnd ?? '11:00')
const nameFilter = ref('')
const rooms = ref<Room[] | null>(null)
const loading = ref(false)
const error = ref('')
const manualName = ref('')
const manualId = ref('')

async function query() {
  loading.value = true
  error.value = ''
  rooms.value = null
  try {
    const names = nameFilter.value
      .split(/[，,、\s]+/)
      .map((s) => s.trim())
      .filter(Boolean)
    const res = await api.roomsAvailable(
      `${date.value}T${start.value}:00+08:00`,
      `${date.value}T${end.value}:00+08:00`,
      names,
    )
    rooms.value = res.rooms
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

function addManual() {
  const name = manualName.value.trim()
  if (!name) return
  emit('add', { id: manualId.value.trim(), name })
  manualName.value = ''
  manualId.value = ''
}
</script>

<template>
  <div class="picker">
    <div class="picker-query">
      <div class="field">
        <label for="picker-date">查询日期</label>
        <input id="picker-date" v-model="date" type="date" />
      </div>
      <div class="field">
        <label for="picker-start">开始</label>
        <input id="picker-start" v-model="start" type="time" />
      </div>
      <div class="field">
        <label for="picker-end">结束</label>
        <input id="picker-end" v-model="end" type="time" />
      </div>
      <div class="field grow">
        <label for="picker-filter">名称筛选（可选，逗号分隔）</label>
        <input id="picker-filter" v-model="nameFilter" type="text" placeholder="如 1,2,3 或 木星,火星" />
      </div>
      <button class="btn" type="button" :disabled="loading" @click="query">
        {{ loading ? '查询中…' : '查可用会议室' }}
      </button>
    </div>

    <p v-if="error" class="picker-error" role="alert">{{ error }}</p>

    <div v-if="rooms !== null" class="picker-results">
      <p v-if="rooms.length === 0" class="picker-empty">
        该时间块下没有可见的会议室。公司内网之外的会议室通常不可见，可在下方手动录入会议室名称，程序会在预约时在线解析。
      </p>
      <ul v-else class="room-list">
        <li v-for="room in rooms" :key="room.id">
          <span class="room-name">{{ room.name }}</span>
          <span v-if="room.capacity" class="room-capacity mono">{{ room.capacity }} 人</span>
          <span class="mono room-id">{{ room.id }}</span>
          <button class="btn btn-sm" type="button" @click="emit('add', { id: room.id, name: room.name })">加入</button>
        </li>
      </ul>
    </div>

    <div class="manual">
      <div class="field grow">
        <label for="manual-name">手动录入名称</label>
        <input id="manual-name" v-model="manualName" type="text" placeholder="如 5 号会议室" />
      </div>
      <div class="field">
        <label for="manual-id">ID（可选 omm_）</label>
        <input id="manual-id" v-model="manualId" type="text" placeholder="留空自动解析" />
      </div>
      <button class="btn" type="button" :disabled="!manualName.trim()" @click="addManual">加入</button>
    </div>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  border: 1px dashed var(--border-strong);
}

.picker-query,
.manual {
  display: flex;
  gap: 10px;
  align-items: flex-end;
  flex-wrap: wrap;
}

.grow {
  flex: 1;
  min-width: 160px;
}

.picker-query .field {
  min-width: 110px;
}

.picker-error {
  margin: 0;
  color: var(--danger);
  font-size: 13px;
}

.picker-empty {
  margin: 0;
  color: var(--text-muted);
  font-size: 13.5px;
  line-height: 1.6;
}

.room-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 220px;
  overflow: auto;
}

.room-list li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface);
  border: 1px solid var(--border);
}

.room-name {
  font-weight: 500;
}

.room-capacity {
  color: var(--text-muted);
  font-size: 12.5px;
}

.room-id {
  margin-left: auto;
  color: var(--text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 220px;
}

.manual {
  border-top: 1px solid var(--border);
  padding-top: 12px;
}
</style>
