<script setup lang="ts">
// CRadioGroup — v-model 承载选中值,子 CRadio 通过 inject 读写。
import { provide } from 'vue'
import { C_RADIO_GROUP } from './radioGroup'

const model = defineModel<string | number | null>({ default: null })

let seq = 0
provide(C_RADIO_GROUP, { model, name: `c-radio-group-${++seq}` })
</script>

<template>
  <div class="c-radio-group" role="radiogroup"><slot /></div>
</template>

<style scoped>
.c-radio-group {
  display: inline-flex;
  align-items: center;
  gap: 14px;
}
</style>
