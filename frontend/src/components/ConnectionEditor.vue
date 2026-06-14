<script setup lang="ts">
// ConnectionEditor — application-level modal hosting ConnectionForm. The
// shell uses NModal (per UI_SPEC §5, this is "application-internal"). The
// macOS Sheet attachment is M4 work.
import { computed } from 'vue'
import { NModal } from 'naive-ui'
import ConnectionForm from './ConnectionForm.vue'
import type { ConnectionProfile, DriverInfo } from '../api/connections'

const props = defineProps<{
  show: boolean
  driver: DriverInfo | null
  initial?: ConnectionProfile | null
}>()
const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'saved', profile: ConnectionProfile): void
}>()

const title = computed(() => {
  if (!props.driver) return '新建连接'
  return props.initial ? `编辑 ${props.driver.name} 连接` : `新建 ${props.driver.name} 连接`
})

function setShow(v: boolean) { emit('update:show', v) }
function onSaved(p: ConnectionProfile) {
  emit('saved', p)
  emit('update:show', false)
}
</script>

<template>
  <n-modal
    :show="show"
    :title="title"
    preset="card"
    size="small"
    style="width: 640px"
    :mask-closable="false"
    :bordered="false"
    @update:show="setShow"
  >
    <ConnectionForm
      v-if="driver"
      :driver="driver"
      :initial="initial ?? null"
      @saved="onSaved"
      @cancel="setShow(false)"
    />
  </n-modal>
</template>
