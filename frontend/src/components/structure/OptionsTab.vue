<script setup lang="ts">
// OptionsTab — table-level options. Currently just the COMMENT clause; future
// options (ENGINE, CHARSET, COLLATE, AUTO_INCREMENT start) can sit alongside.
import type { TableOptionsDraft } from '../../lib/alterPlan'

const props = defineProps<{
  modelValue: TableOptionsDraft
  busy?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: TableOptionsDraft): void
}>()

function commit() {
  emit('update:modelValue', props.modelValue)
}
</script>

<template>
  <div class="opts-tab">
    <div class="opt-field">
      <label class="opt-label">{{ $t('structure.options.tableComment') }}</label>
      <textarea
        v-model="modelValue.comment"
        class="opt-textarea"
        rows="3"
        :disabled="busy"
        :placeholder="$t('structure.options.commentPlaceholder')"
        @input="commit"
      />
    </div>
  </div>
</template>

<style scoped>
.opts-tab {
  padding: 12px;
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  background-color: var(--catdb-surface-content);
}
.opt-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.opt-label {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
}
/* 自绘 textarea:照 CInput 的形态(surface-content 底 + 1px inset 描边 + accent 聚焦) */
.opt-textarea {
  width: 100%;
  min-height: 60px;
  max-height: 160px;
  padding: 5px 9px;
  border: none;
  outline: none;
  resize: vertical;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  box-shadow: inset 0 0 0 1px var(--catdb-control-border);
  color: var(--catdb-text-primary);
  font-family: var(--catdb-font-family);
  font-size: var(--catdb-fs-small);
  line-height: 1.5;
  user-select: text;
  -webkit-user-select: text;
  transition: box-shadow 130ms ease-out;
}
.opt-textarea:focus {
  box-shadow: inset 0 0 0 1px var(--catdb-accent), var(--catdb-focus-ring);
}
.opt-textarea::placeholder {
  color: var(--catdb-text-tertiary);
}
.opt-textarea:disabled {
  opacity: 0.4;
}
</style>
