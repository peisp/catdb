<script setup lang="ts">
// AgentGrants — the session authorization row (§5 gate 3, §10.2). A compact
// line of checkboxes above the composer, Agent mode only. SELECT is always on
// and disabled (read is the baseline); INSERT / UPDATE / DELETE / DDL toggle
// the session grants (values lowercase). On a prod connection every write
// toggle is disabled + greyed with a tooltip (gate 1 hard read-only surfaced
// in the UI).
import { computed } from 'vue'
import { NCheckbox, NTooltip } from 'naive-ui'
import { t } from '../../i18n'

const props = defineProps<{ grants: string[]; readonly: boolean }>()
const emit = defineEmits<{ (e: 'update', grants: string[]): void }>()

// SELECT is implicit and never toggled.
const WRITE = ['insert', 'update', 'delete', 'ddl'] as const

const set = computed(() => new Set(props.grants.map((g) => g.toLowerCase())))

function label(v: string): string {
  return v === 'ddl' ? 'DDL' : v.toUpperCase()
}

function toggle(v: string, on: boolean) {
  if (props.readonly) return
  const next = new Set(props.grants.map((g) => g.toLowerCase()))
  if (on) next.add(v)
  else next.delete(v)
  // Keep SELECT out of the persisted set unless it was already there — the
  // backend treats read as always allowed; we only ship the write grants.
  emit('update', [...next])
}
</script>

<template>
  <div class="grants">
    <span class="lead">{{ $t('agent.grants.label') }}</span>
    <n-checkbox size="small" :checked="true" :disabled="true" class="grant">SELECT</n-checkbox>
    <template v-for="v in WRITE" :key="v">
      <n-tooltip v-if="readonly" trigger="hover">
        <template #trigger>
          <span class="grant-wrap">
            <n-checkbox size="small" :checked="set.has(v)" :disabled="true" class="grant">{{ label(v) }}</n-checkbox>
          </span>
        </template>
        {{ $t('agent.grants.prodDisabled') }}
      </n-tooltip>
      <n-checkbox
        v-else
        size="small"
        :checked="set.has(v)"
        class="grant"
        @update:checked="(on: boolean) => toggle(v, on)"
      >{{ label(v) }}</n-checkbox>
    </template>
  </div>
</template>

<style scoped>
.grants {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  padding: 5px 8px 0;
}
.lead {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-secondary);
  margin-right: 2px;
}
.grant :deep(.n-checkbox__label) { font-size: var(--catdb-fs-mini); padding-left: 4px; }
.grant-wrap { display: inline-flex; }
</style>
