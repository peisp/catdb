<script setup lang="ts">
// LaunchAuthDialog — the password prompt a `-con` command line triggers when
// it wants to connect and no password is stored (docs/CLI_CON_DESIGN.md §6.2).
//
// Windows shows a NATIVE dialog for this (external launchers drive it through
// the Win32 API), so this modal is the fallback everywhere else — and the
// normal experience for anyone who simply typed a `-con` by hand.
//
// The backend is BLOCKED on the answer, so every exit path — confirm, cancel,
// Esc, backdrop — reports back; a dismissed dialog that stayed silent would
// pin the launch until its five-minute timeout.
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CButton, CInput, CModal } from '../ui'
import { system as systemApi } from '../../api'
import type { PasswordRequestPayload } from '../../api/system'

const request = ref<PasswordRequestPayload | null>(null)
const password = ref('')
const show = ref(false)
const inputRef = ref<InstanceType<typeof CInput> | null>(null)

let off: (() => void) | null = null
onMounted(() => {
  off = systemApi.onPasswordRequest((p) => {
    request.value = p
    password.value = ''
    show.value = true
    void nextTick(() => inputRef.value?.focus())
  })
})
onBeforeUnmount(() => {
  off?.()
  off = null
})

// CModal closes itself on Esc / backdrop click; catch that as a cancel.
watch(show, (visible) => {
  if (!visible && request.value) answer('', false)
})

function answer(pwd: string, ok: boolean) {
  const req = request.value
  request.value = null
  password.value = ''
  show.value = false
  if (!req) return
  void systemApi.submitLaunchPassword(req.requestId, pwd, ok)
}

function onConfirm() {
  answer(password.value, true)
}
function onCancel() {
  answer('', false)
}
</script>

<template>
  <CModal v-model:show="show" :width="380">
    <div class="modal-title">{{ $t('launchAuth.title', { name: request?.name ?? '' }) }}</div>
    <div class="field">
      <div class="label">{{ $t('launchAuth.user') }}</div>
      <div class="user">{{ request?.user || '—' }}</div>
    </div>
    <div class="field">
      <div class="label">{{ $t('launchAuth.password') }}</div>
      <CInput
        ref="inputRef"
        v-model="password"
        type="password"
        size="small"
        autocomplete="off"
        @keydown.enter.prevent="onConfirm"
      />
    </div>
    <div class="actions">
      <CButton size="small" @click="onCancel">{{ $t('common.cancel') }}</CButton>
      <CButton size="small" variant="primary" @click="onConfirm">{{ $t('common.ok') }}</CButton>
    </div>
  </CModal>
</template>

<style scoped>
.modal-title {
  font-size: var(--catdb-fs-body);
  font-weight: 600;
  color: var(--catdb-text-primary);
  margin-bottom: 12px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 10px;
}
.label {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
}
.user {
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 14px;
}
</style>
