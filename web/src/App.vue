<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from './api'
import type { Attempt, BookingRecord, Rule, Session } from './types'
import RuleCard from './components/RuleCard.vue'
import RuleEditor from './components/RuleEditor.vue'
import SessionStrip from './components/SessionStrip.vue'
import RecordsTable from './components/RecordsTable.vue'
import AttemptsLog from './components/AttemptsLog.vue'

const rules = ref<Rule[]>([])
const records = ref<BookingRecord[]>([])
const sessions = ref<Session[]>([])
const attempts = ref<Attempt[]>([])
const health = ref<{
  ok: boolean
  lark: { ok: boolean; error: string; identity: { userName: string } }
  now: string
} | null>(null)
const loadError = ref('')
const editorOpen = ref(false)
const editingRule = ref<Rule | null>(null)

const runningCount = computed(() => sessions.value.filter((s) => s.status === 'running').length)

async function refresh() {
  try {
    const [r, rec, s, a] = await Promise.all([
      api.listRules(),
      api.records(),
      api.sessions(),
      api.attempts(80),
    ])
    rules.value = r
    records.value = rec
    sessions.value = s
    attempts.value = a
    loadError.value = ''
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err)
  }
}

async function refreshHealth() {
  try {
    health.value = await api.health()
  } catch {
    health.value = null
  }
}

let timer: number | undefined
let healthTimer: number | undefined

onMounted(() => {
  refresh()
  refreshHealth()
  timer = window.setInterval(refresh, 4000)
  healthTimer = window.setInterval(refreshHealth, 30000)
})

onUnmounted(() => {
  window.clearInterval(timer)
  window.clearInterval(healthTimer)
})

function openCreate() {
  editingRule.value = null
  editorOpen.value = true
}

function openEdit(rule: Rule) {
  editingRule.value = rule
  editorOpen.value = true
}

function onEditorSaved() {
  editorOpen.value = false
  refresh()
}
</script>

<template>
  <div class="container">
    <header class="app-header">
      <div class="brand">
        <h1>会议室预约台</h1>
        <p class="tagline">飞书会议室预约：循环规则 + 提前 30 天定时，10 分钟窗口每分钟一次</p>
      </div>
      <div class="header-status">
        <span v-if="health?.lark.ok" class="badge badge-booked" title="lark-cli 授权正常">
          {{ health.lark.identity.userName }}
        </span>
        <span v-else class="badge badge-failed" :title="health?.lark.error ?? '无法连接 lark-cli'">
          lark-cli 异常
        </span>
        <span v-if="runningCount > 0" class="badge badge-running">预约进行中 {{ runningCount }}</span>
        <button class="btn btn-primary" type="button" @click="openCreate">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
          </svg>
          新建规则
        </button>
      </div>
    </header>

    <p v-if="loadError" class="load-error" role="alert">无法连接服务端：{{ loadError }}</p>

    <SessionStrip :sessions="sessions" />

    <section class="section" aria-labelledby="rules-heading">
      <div class="section-head">
        <h2 id="rules-heading">预约规则</h2>
        <span class="section-note">{{ rules.length }} 条，启用 {{ rules.filter((r) => r.enabled).length }} 条</span>
      </div>
      <div v-if="rules.length === 0" class="empty panel">
        <p>还没有循环预约规则。</p>
        <p class="empty-hint">新建一条规则：会议主题、首次目标日期、循环规则（不循环/每天/每周/每月/每季度）、会议时间与会议室优先级。</p>
        <button class="btn btn-primary" type="button" @click="openCreate">创建第一条规则</button>
      </div>
      <div v-else class="rules-grid">
        <RuleCard
          v-for="rule in rules"
          :key="rule.id"
          :rule="rule"
          :records="records.filter((r) => r.ruleId === rule.id)"
          @edit="openEdit"
          @changed="refresh"
        />
      </div>
    </section>

    <section class="section" aria-labelledby="records-heading">
      <div class="section-head">
        <h2 id="records-heading">预约记录</h2>
        <span class="section-note">已约与占用日期，后续循环自动跳过</span>
      </div>
      <RecordsTable :records="records" @changed="refresh" />
    </section>

    <section class="section" aria-labelledby="log-heading">
      <AttemptsLog :attempts="attempts" />
    </section>

    <RuleEditor
      v-if="editorOpen"
      :rule="editingRule"
      @close="editorOpen = false"
      @saved="onEditorSaved"
    />
  </div>
</template>

<style scoped>
.app-header {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  padding: 34px 0 22px;
  border-bottom: 1px solid var(--border);
}

.brand h1 {
  font-size: 26px;
  line-height: 1.2;
}

.tagline {
  margin: 6px 0 0;
  color: var(--text-muted);
  font-size: 13.5px;
}

.header-status {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.load-error {
  margin: 16px 0 0;
  padding: 10px 14px;
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 14px;
}

.section {
  margin-top: 34px;
}

.section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.section-head h2 {
  font-size: 17px;
}

.section-note {
  color: var(--text-muted);
  font-size: 13px;
}

.rules-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 14px;
}

.empty {
  padding: 34px 26px;
  text-align: center;
  color: var(--text-muted);
}

.empty p {
  margin: 0 0 6px;
}

.empty-hint {
  font-size: 13.5px;
  max-width: 520px;
  margin: 0 auto 16px;
}
</style>
