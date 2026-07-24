<script setup lang="ts">
// AgentPlanCard — a task plan awaiting approval (§6 task contract). Shows the
// goal, the ordered statement list (each keyword-highlighted), and the
// estimated impact. Approve / reject (with optional inline reason) mirror the
// approval card; once decided the card freezes into a status line.
import { computed, ref } from 'vue'
import { highlightSql } from './markdown'
import type { PlanEntry } from './types'

const props = defineProps<{ entry: PlanEntry }>()
const emit = defineEmits<{
  (e: 'approve'): void
  (e: 'reject', reason: string): void
}>()

const pending = computed(() => props.entry.status === 'pending')
const stmts = computed(() => props.entry.statements.map((s) => ({ raw: s, html: highlightSql(s) })))

const rejecting = ref(false)
const reason = ref('')

function doReject() {
  emit('reject', reason.value.trim())
}
</script>

<template>
  <div class="plan-card" :class="{ decided: !pending }">
    <div class="head">
      <span class="badge">{{ $t('agent.plan.badge') }}</span>
      <span class="goal">{{ entry.goal }}</span>
    </div>

    <div class="section-label">{{ $t('agent.plan.statements') }}</div>
    <ol class="stmts">
      <li v-for="(s, i) in stmts" :key="i">
        <pre class="sql mono"><code v-html="s.html" /></pre>
      </li>
    </ol>

    <div v-if="entry.impact" class="impact">
      <span class="impact-label">{{ $t('agent.plan.impact') }}</span>
      <span class="impact-text">{{ entry.impact }}</span>
    </div>

    <template v-if="pending">
      <div v-if="!rejecting" class="actions">
        <button type="button" class="btn primary" @click="emit('approve')">{{ $t('agent.plan.approve') }}</button>
        <button type="button" class="btn ghost" @click="rejecting = true">{{ $t('agent.plan.reject') }}</button>
      </div>
      <div v-else class="reject-box">
        <textarea
          v-model="reason"
          class="reason mono"
          rows="2"
          :placeholder="$t('agent.plan.reasonPlaceholder')"
        />
        <div class="actions">
          <button type="button" class="btn ghost" @click="rejecting = false">{{ $t('common.cancel') }}</button>
          <button type="button" class="btn danger" @click="doReject">{{ $t('agent.plan.confirmReject') }}</button>
        </div>
      </div>
    </template>

    <div v-else class="status" :class="entry.status">
      <template v-if="entry.status === 'approved'">{{ $t('agent.plan.statusApproved') }}</template>
      <template v-else>
        {{ entry.reason ? $t('agent.plan.statusRejectedReason', { reason: entry.reason }) : $t('agent.plan.statusRejected') }}
      </template>
    </div>
  </div>
</template>

<style scoped>
.plan-card {
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  margin: 6px 0;
  padding: 8px;
  overflow: hidden;
}
.plan-card.decided { opacity: 0.85; }

.head { display: flex; align-items: baseline; gap: 6px; margin-bottom: 8px; }
.badge {
  flex: 0 0 auto;
  font-size: var(--catdb-fs-mini);
  font-weight: 600;
  line-height: 1;
  padding: 2px 6px;
  border-radius: var(--catdb-rounded-sm);
  color: var(--catdb-accent);
  background: var(--catdb-accent-soft);
}
.goal {
  font-size: var(--catdb-fs-body);
  font-weight: 600;
  color: var(--catdb-text-primary);
  word-break: break-word;
}

.section-label {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-secondary);
  margin: 4px 0 4px;
}
.stmts { margin: 0; padding-left: 20px; }
.stmts li { margin: 0 0 4px; }
.sql {
  margin: 0;
  padding: 5px 7px;
  border-radius: var(--catdb-rounded-xs);
  background: var(--catdb-surface-chrome);
  overflow-x: auto;
  font-size: var(--catdb-fs-mono-small);
  line-height: 1.5;
  color: var(--catdb-text-primary);
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.sql :deep(.kw) { color: var(--catdb-accent); font-weight: 600; }

.impact {
  margin-top: 6px;
  font-size: var(--catdb-fs-small);
}
.impact-label { color: var(--catdb-text-secondary); margin-right: 4px; }
.impact-text { color: var(--catdb-text-primary); }

.actions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
.btn {
  border: 1px solid var(--catdb-control-border);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  font: inherit;
  font-size: var(--catdb-fs-small);
  height: 24px;
  padding: 0 10px;
  border-radius: var(--catdb-rounded-sm);
  cursor: default;
  transition: background 130ms ease-out;
}
.btn:hover { background: var(--catdb-hover-fill); }
.btn.primary { border-color: transparent; background: var(--catdb-accent); color: var(--catdb-text-on-accent); }
.btn.primary:hover { background: var(--catdb-accent-pressed); }
.btn.danger { border-color: transparent; background: var(--catdb-error); color: var(--catdb-text-on-accent); }
.btn.ghost { border-color: transparent; background: transparent; color: var(--catdb-text-secondary); }
.btn.ghost:hover { background: var(--catdb-hover-fill); color: var(--catdb-text-primary); }

.reject-box { margin-top: 8px; }
.reason {
  width: 100%;
  resize: none;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  font-family: inherit;
  font-size: var(--catdb-fs-small);
  line-height: 1.4;
  padding: 5px 7px;
  outline: none;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.reason:focus { border-color: var(--catdb-accent); }

.status { margin-top: 6px; font-size: var(--catdb-fs-small); color: var(--catdb-text-secondary); }
.status.rejected { color: var(--catdb-error); }
</style>
