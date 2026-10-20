<script setup lang="ts">
import { ref } from 'vue'
import type { Attempt } from '../types'

const props = defineProps<{ attempts: Attempt[] }>()
const open = ref(false)

const kindMeta: Record<string, { label: string; cls: string }> = {
  'session-start': { label: '开始', cls: 'k-start' },
  find: { label: '查房', cls: 'k-find' },
  create: { label: '预约', cls: 'k-create' },
  skip: { label: '跳过', cls: 'k-skip' },
  'session-end': { label: '结束', cls: 'k-end' },
}

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('zh-CN', { hour12: false })
}
</script>

<template>
  <div class="panel log">
    <button class="log-toggle" type="button" :aria-expanded="open" @click="open = !open">
      <h2 id="log-heading" class="log-title">引擎日志</h2>
      <span class="log-count mono">{{ props.attempts.length }} 条</span>
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        aria-hidden="true"
        class="log-chevron"
        :class="{ open }"
      >
        <path d="m6 9 6 6 6-6" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
    <div v-if="open" class="log-body">
      <p v-if="props.attempts.length === 0" class="log-empty">暂无日志。</p>
      <ul v-else class="log-list">
        <li v-for="a in props.attempts" :key="a.id" :class="{ bad: !a.ok }">
          <span class="log-time mono">{{ fmtTime(a.at) }}</span>
          <span class="log-kind" :class="kindMeta[a.kind]?.cls">{{ kindMeta[a.kind]?.label ?? a.kind }}</span>
          <span class="log-rule">{{ a.ruleName }}</span>
          <span class="log-date mono">{{ a.date }}</span>
          <span class="log-detail" :title="a.detail">{{ a.detail }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.log {
  overflow: hidden;
}

.log-toggle {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 18px;
  background: transparent;
  border: none;
  color: var(--text);
  font: inherit;
  cursor: pointer;
}

.log-toggle:hover {
  background: var(--surface-2);
}

.log-toggle:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.log-title {
  font-size: 17px;
}

.log-count {
  color: var(--text-muted);
  font-size: 12.5px;
}

.log-chevron {
  margin-left: auto;
  color: var(--text-muted);
  transition: transform 160ms ease;
}

.log-chevron.open {
  transform: rotate(180deg);
}

.log-body {
  border-top: 1px solid var(--border);
  max-height: 300px;
  overflow-y: auto;
}

.log-empty {
  margin: 0;
  padding: 18px;
  color: var(--text-muted);
  font-size: 13.5px;
}

.log-list {
  list-style: none;
  margin: 0;
  padding: 6px 0;
}

.log-list li {
  display: flex;
  align-items: baseline;
  gap: 12px;
  padding: 5px 18px;
  font-size: 13px;
}

.log-list li.bad .log-detail {
  color: var(--danger);
}

.log-time {
  color: var(--text-muted);
  flex: none;
}

.log-kind {
  flex: none;
  font-size: 12px;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--border-strong);
  color: var(--text-muted);
}

.k-create {
  color: var(--success);
  border-color: rgba(52, 199, 123, 0.4);
}

.k-find {
  color: var(--accent);
  border-color: rgba(76, 125, 255, 0.4);
}

.k-skip {
  color: var(--warning);
  border-color: rgba(232, 163, 61, 0.4);
}

.k-end {
  color: var(--danger);
  border-color: rgba(229, 72, 77, 0.4);
}

.log-rule {
  flex: none;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-date {
  flex: none;
  color: var(--text-muted);
  font-size: 12px;
}

.log-detail {
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
