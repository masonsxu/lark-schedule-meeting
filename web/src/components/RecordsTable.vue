<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api'
import type { BookingRecord } from '../types'

defineProps<{ records: BookingRecord[] }>()
const emit = defineEmits<{ changed: [] }>()

const expandedId = ref('')
const busyId = ref('')
const error = ref('')

const statusMeta: Record<string, { label: string; cls: string }> = {
  booked: { label: '已预约', cls: 'badge-booked' },
  occupied: { label: '无房可抢', cls: 'badge-occupied' },
  failed: { label: '失败', cls: 'badge-failed' },
}

const sourceName: Record<string, string> = {
  auto: '自动',
  manual: '手动',
  detected: '检测',
}

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' })
}

async function remove(rec: BookingRecord, deleteEvent: boolean) {
  const scope = deleteEvent && rec.eventId ? '记录与飞书日程' : '记录（保留飞书日程）'
  if (!window.confirm(`删除${scope}？日期 ${rec.date} 将重新允许抢座。`)) return
  busyId.value = rec.id
  error.value = ''
  try {
    await api.deleteRecord(rec.id, deleteEvent)
    emit('changed')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busyId.value = ''
  }
}
</script>

<template>
  <div class="panel table-wrap">
    <p v-if="error" class="table-error" role="alert">{{ error }}</p>
    <table v-if="records.length > 0">
      <thead>
        <tr>
          <th scope="col">日期</th>
          <th scope="col">规则</th>
          <th scope="col">时间</th>
          <th scope="col">会议室</th>
          <th scope="col">状态</th>
          <th scope="col">尝试</th>
          <th scope="col">操作</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="rec in records" :key="rec.id">
          <tr @click="expandedId = expandedId === rec.id ? '' : rec.id">
            <td class="mono">{{ rec.date }}</td>
            <td class="cell-rule">{{ rec.ruleName }}</td>
            <td class="mono">{{ fmtTime(rec.start) }}–{{ fmtTime(rec.end) }}</td>
            <td>{{ rec.roomName ?? (rec.status === 'booked' ? '（无房会议）' : '—') }}</td>
            <td>
              <span class="badge" :class="statusMeta[rec.status]?.cls">
                {{ statusMeta[rec.status]?.label ?? rec.status }}
              </span>
            </td>
            <td class="mono">{{ rec.attempts }}</td>
            <td class="cell-actions" @click.stop>
              <button
                v-if="rec.eventId"
                class="btn btn-sm btn-danger"
                type="button"
                :disabled="busyId === rec.id"
                title="删除飞书日程并清除记录"
                @click="remove(rec, true)"
              >
                撤销预约
              </button>
              <button
                class="btn btn-sm btn-ghost"
                type="button"
                :disabled="busyId === rec.id"
                title="仅清除记录，飞书日程保留"
                @click="remove(rec, false)"
              >
                仅删记录
              </button>
            </td>
          </tr>
          <tr v-if="expandedId === rec.id" class="detail-row">
            <td colspan="7">
              <dl class="detail">
                <div><dt>来源</dt><dd>{{ sourceName[rec.source] ?? rec.source }}</dd></div>
                <div v-if="rec.eventId"><dt>日程</dt><dd class="mono">{{ rec.eventId }}</dd></div>
                <div v-if="rec.roomId"><dt>会议室 ID</dt><dd class="mono">{{ rec.roomId }}</dd></div>
                <div class="detail-note"><dt>备注</dt><dd>{{ rec.note ?? '—' }}</dd></div>
              </dl>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
    <div v-else class="table-empty">
      <p>暂无预约记录。规则触发后，这里会出现每天的预约结果与跳过原因。</p>
    </div>
  </div>
</template>

<style scoped>
.table-wrap {
  overflow-x: auto;
}

.table-error {
  margin: 0;
  padding: 10px 14px;
  color: var(--danger);
  font-size: 13.5px;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13.5px;
}

th,
td {
  text-align: left;
  padding: 9px 12px;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}

th {
  color: var(--text-muted);
  font-weight: 500;
  font-size: 12.5px;
}

tbody tr {
  cursor: pointer;
}

tbody tr:hover {
  background: var(--surface-2);
}

.cell-rule {
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.cell-actions {
  display: flex;
  gap: 6px;
}

.detail-row td {
  background: var(--surface-2);
}

.detail {
  margin: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 6px 28px;
  white-space: normal;
}

.detail > div {
  display: flex;
  gap: 8px;
  font-size: 13px;
}

.detail dt {
  color: var(--text-muted);
}

.detail dd {
  margin: 0;
}

.detail-note {
  flex-basis: 100%;
}

.table-empty {
  padding: 26px;
  color: var(--text-muted);
  font-size: 14px;
  text-align: center;
}

.table-empty p {
  margin: 0;
}
</style>
