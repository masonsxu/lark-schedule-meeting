<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api'
import type { Room } from '../types'

const emit = defineEmits<{ add: [room: { id: string; name: string }] }>()

const props = defineProps<{
  /** 用于检索可用性的默认时间（HH:MM）。 */
  defaultStart?: string
  defaultEnd?: string
}>()

const tomorrow = (() => {
  const d = new Date(Date.now() + 24 * 3600 * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})()

// ---- 本地名录 ----
const rooms = ref<Room[]>([])
const roomsLoading = ref(false)
const manualID = ref('')
const manualName = ref('')
const copiedID = ref('')
const error = ref('')

async function refreshRooms() {
  roomsLoading.value = true
  try {
    rooms.value = await api.rooms()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    roomsLoading.value = false
  }
}

onMounted(refreshRooms)

async function copyID(id: string) {
  try {
    await navigator.clipboard.writeText(id)
    copiedID.value = id
    setTimeout(() => {
      if (copiedID.value === id) copiedID.value = ''
    }, 1500)
  } catch {
    // 剪贴板不可用时降级为全选提示。
    window.prompt('复制会议室 ID', id)
  }
}

async function addManual() {
  const id = manualID.value.trim()
  if (!id) return
  try {
    await api.addRoom(id, manualName.value.trim())
    manualID.value = ''
    manualName.value = ''
    await refreshRooms()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

async function removeRoom(id: string) {
  try {
    await api.deleteRoom(id)
    await refreshRooms()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

// ---- 日期 + 时间检索（结果自动入库）----
const date = ref(tomorrow)
const start = ref(props.defaultStart ?? '10:00')
const end = ref(props.defaultEnd ?? '11:00')
const nameFilter = ref('')
const found = ref<Room[] | null>(null)
const searching = ref(false)
const searchError = ref('')

async function query() {
  searching.value = true
  searchError.value = ''
  found.value = null
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
    found.value = res.rooms
    await refreshRooms() // 检索结果已自动存入名录
  } catch (err) {
    searchError.value = err instanceof Error ? err.message : String(err)
  } finally {
    searching.value = false
  }
}
</script>

<template>
  <div class="panel-room">
    <p v-if="error" class="panel-error" role="alert">{{ error }}</p>

    <!-- 名录 -->
    <div class="section-head">
      <h4>会议室名录</h4>
      <span class="section-note">ID 固定不变，一次录入长期使用</span>
    </div>
    <p v-if="roomsLoading && rooms.length === 0" class="hint">加载中…</p>
    <p v-else-if="rooms.length === 0" class="hint">
      名录为空：通过下方检索自动入库，或直接手动录入会议室 ID（omm_ 开头）。
    </p>
    <ul v-else class="room-list">
      <li v-for="room in rooms" :key="room.id">
        <div class="room-main">
          <span class="room-name">{{ room.name || '（未命名）' }}</span>
          <button
            class="room-id mono"
            type="button"
            :title="'点击复制 ' + room.id"
            @click="copyID(room.id)"
          >
            {{ copiedID === room.id ? '已复制' : room.id }}
          </button>
          <span v-if="room.capacity" class="room-capacity mono">{{ room.capacity }} 人</span>
        </div>
        <div class="room-ops">
          <button class="btn btn-sm" type="button" @click="emit('add', { id: room.id, name: room.name })">加入</button>
          <button class="btn btn-sm btn-ghost" type="button" aria-label="从名录删除" @click="removeRoom(room.id)">×</button>
        </div>
      </li>
    </ul>

    <!-- 手动录入 -->
    <div class="manual">
      <div class="field grow">
        <label for="manual-id">手动录入 ID（omm_ 开头，必填）</label>
        <input id="manual-id" v-model="manualID" type="text" placeholder="omm_xxxxxxxx" />
      </div>
      <div class="field">
        <label for="manual-name">名称（可选）</label>
        <input id="manual-name" v-model="manualName" type="text" placeholder="如 5 号会议室" />
      </div>
      <button class="btn" type="button" :disabled="!manualID.trim()" @click="addManual">录入名录</button>
    </div>

    <!-- 检索 -->
    <div class="section-head">
      <h4>按日期时间检索</h4>
      <span class="section-note">结果自动存入名录；列表可能不全，仅作辅助</span>
    </div>
    <div class="picker-query">
      <div class="field">
        <label for="picker-date">日期</label>
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
        <label for="picker-filter">名称筛选（可选）</label>
        <input id="picker-filter" v-model="nameFilter" type="text" placeholder="如 1,2,3" />
      </div>
      <button class="btn" type="button" :disabled="searching" @click="query">
        {{ searching ? '检索中…' : '检索可用会议室' }}
      </button>
    </div>
    <p v-if="searchError" class="panel-error" role="alert">{{ searchError }}</p>
    <p v-if="found !== null && found.length === 0" class="hint">
      该时间块未检索到会议室。飞书检索接口返回的列表可能不全（日历上空闲的会议室也可能不出现），
      请直接手动录入已知 ID。
    </p>
    <ul v-if="found !== null && found.length > 0" class="room-list found">
      <li v-for="room in found" :key="room.id">
        <div class="room-main">
          <span class="room-name">{{ room.name }}</span>
          <button class="room-id mono" type="button" :title="'点击复制 ' + room.id" @click="copyID(room.id)">
            {{ copiedID === room.id ? '已复制' : room.id }}
          </button>
        </div>
        <button class="btn btn-sm" type="button" @click="emit('add', { id: room.id, name: room.name })">加入</button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.panel-room {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  border: 1px dashed var(--border-strong);
}

.section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
}

.section-head h4 {
  margin: 0;
  font-size: 13.5px;
  font-weight: 600;
}

.section-note {
  color: var(--text-muted);
  font-size: 12px;
}

.hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.6;
}

.panel-error {
  margin: 0;
  color: var(--danger);
  font-size: 13px;
}

.room-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 240px;
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

.room-list.found li {
  border-color: rgba(76, 125, 255, 0.35);
}

.room-main {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1;
}

.room-name {
  font-weight: 500;
  white-space: nowrap;
}

/* ID 高亮：等宽、主色、可点击复制 */
.room-id {
  border: 1px solid rgba(76, 125, 255, 0.45);
  background: var(--accent-soft);
  color: var(--accent);
  border-radius: var(--radius-sm);
  padding: 2px 8px;
  font-size: 12px;
  cursor: pointer;
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.room-id:hover {
  border-color: var(--accent);
}

.room-id:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.room-capacity {
  color: var(--text-muted);
  font-size: 12px;
}

.room-ops {
  display: flex;
  gap: 4px;
  margin-left: auto;
}

.manual,
.picker-query {
  display: flex;
  gap: 10px;
  align-items: flex-end;
  flex-wrap: wrap;
}

.picker-query {
  padding-top: 2px;
}

.grow {
  flex: 1;
  min-width: 150px;
}

.picker-query .field {
  min-width: 100px;
}
</style>
