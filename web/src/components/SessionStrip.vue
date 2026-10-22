<script setup lang="ts">
import { computed } from 'vue'
import type { Session } from '../types'

const props = defineProps<{ sessions: Session[] }>()

const running = computed(() => props.sessions.filter((s) => s.status === 'running'))
const finished = computed(() => props.sessions.filter((s) => s.status !== 'running').slice(0, 3))

const statusName: Record<string, string> = {
  booked: '已约到',
  occupied: '无房可约',
  failed: '失败',
}

function countdown(deadline: string): string {
  const ms = new Date(deadline).getTime() - Date.now()
  if (ms <= 0) return '00:00'
  const total = Math.floor(ms / 1000)
  const mm = String(Math.floor(total / 60)).padStart(2, '0')
  const ss = String(total % 60).padStart(2, '0')
  return `${mm}:${ss}`
}

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
</script>

<template>
  <div v-if="running.length > 0 || finished.length > 0" class="strip panel" aria-live="polite">
    <div v-for="s in running" :key="s.ruleId + s.date" class="strip-item running">
      <div class="strip-main">
        <span class="badge badge-running">预约中</span>
        <span class="strip-title">{{ s.ruleName }}</span>
        <span class="mono strip-date">{{ s.date }}</span>
      </div>
      <!-- 签名元素：逐分钟尝试刻度 -->
      <div class="dots" :aria-label="`已尝试 ${s.attemptsDone} / ${s.maxAttempts} 次`">
        <span
          v-for="i in s.maxAttempts"
          :key="i"
          class="dot"
          :class="{ done: i <= s.attemptsDone, next: i === s.attemptsDone + 1 }"
        />
      </div>
      <div class="strip-side">
        <span class="mono countdown" :title="`窗口截止 ${fmtTime(s.deadline)}`">{{ countdown(s.deadline) }}</span>
        <span class="strip-msg">{{ s.lastMessage }}</span>
      </div>
    </div>
    <div v-for="s in finished" :key="'f' + s.ruleId + s.date" class="strip-item finished">
      <span class="badge" :class="'badge-' + s.status">{{ statusName[s.status] ?? s.status }}</span>
      <span class="strip-title">{{ s.ruleName }}</span>
      <span class="mono strip-date">{{ s.date }}</span>
      <span class="strip-msg">{{ s.lastMessage }}</span>
    </div>
  </div>
</template>

<style scoped>
.strip {
  margin-top: 22px;
  padding: 6px 4px;
  display: flex;
  flex-direction: column;
}

.strip-item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 10px 14px;
  border-radius: var(--radius-sm);
}

.strip-item.running {
  background: linear-gradient(90deg, rgba(76, 125, 255, 0.1), transparent 70%);
}

.strip-item.finished {
  color: var(--text-muted);
  font-size: 14px;
}

.strip-main {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.strip-title {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.strip-date {
  color: var(--text-muted);
  font-size: 13px;
}

/* 逐分钟刻度点：已尝试=实心蓝，下一次=呼吸，未到=暗点 */
.dots {
  display: flex;
  gap: 5px;
  margin-left: auto;
}

.dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--surface-3);
  border: 1px solid var(--border-strong);
}

.dot.done {
  background: var(--accent);
  border-color: var(--accent);
}

.dot.next {
  border-color: var(--accent);
  animation: pulse 1.6s ease-in-out infinite;
}

@keyframes pulse {
  0%,
  100% {
    box-shadow: 0 0 0 0 rgba(76, 125, 255, 0.5);
  }
  50% {
    box-shadow: 0 0 0 4px rgba(76, 125, 255, 0.14);
  }
}

.strip-side {
  display: flex;
  align-items: center;
  gap: 12px;
}

.countdown {
  font-size: 15px;
  color: var(--accent);
  min-width: 52px;
  text-align: right;
}

.strip-msg {
  color: var(--text-muted);
  font-size: 13px;
  max-width: 320px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 720px) {
  .strip-item {
    flex-wrap: wrap;
  }
  .dots {
    margin-left: 0;
    order: 3;
    width: 100%;
  }
  .strip-msg {
    max-width: 100%;
  }
}
</style>
