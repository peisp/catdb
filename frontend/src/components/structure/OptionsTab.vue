<script setup lang="ts">
// OptionsTab — table-level options. Currently just the COMMENT clause; future
// options (ENGINE, CHARSET, COLLATE, AUTO_INCREMENT start) can sit alongside.
import { NForm, NFormItem, NInput } from 'naive-ui'
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
    <n-form label-placement="top" size="small" :show-feedback="false">
      <n-form-item :label="$t('structure.options.tableComment')">
        <n-input
          v-model:value="modelValue.comment"
          type="textarea"
          :autosize="{ minRows: 3, maxRows: 8 }"
          :disabled="busy"
          :placeholder="$t('structure.options.commentPlaceholder')"
          @update:value="commit"
        />
      </n-form-item>
    </n-form>
  </div>
</template>

<style scoped>
.opts-tab {
  padding: 12px;
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  margin: 6px 6px;
  background-color: var(--app-content-bg);
}
</style>
