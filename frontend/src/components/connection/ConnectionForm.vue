<script setup lang="ts">
// ConnectionForm — renders connection fields *dynamically* from the driver's
// ConnectionSchema(). Adding a new field on the Go side surfaces here with
// no edits required (CLAUDE.md / ARCHITECTURE.md §3.1).
//
// The form maintains a flat draft object (ConnectionDraft) on top of the
// schema by walking dotted keys: "ssl.mode" → draft.ssl.mode, etc. Secrets
// (.password, sshTunnel.password, sshTunnel.privateKeyPass) are routed to
// the dedicated top-level fields so they hit the keyring path on Save.
//
// The left rail hosts the driver-type picker (currently mysql only). When
// editing an existing profile the picker is locked to the profile's driver
// — swapping driver mid-edit would orphan saved credentials.
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NCheckbox,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSpin,
  NTabPane,
  NTabs,
  useMessage,
} from 'naive-ui'
import type { ConnectionDraft, ConnectionProfile, DriverInfo } from '../../api/connections'
import { useConnectionsStore } from '../../stores/connections'
import { t, i18n } from '../../i18n'

// Localize a driver-provided schema string (group/label/help) by key, falling
// back to the driver's own (English baseline) text when no translation exists.
// Keeps the driver locale-agnostic while still localizing the MySQL fields.
function trOr(key: string, fallback: string): string {
  return i18n.global.te(key) ? (i18n.global.t(key) as string) : fallback
}
function groupLabel(g: string): string {
  return trOr(`connection.form.groups.${g}`, g)
}
function fieldLabel(f: { key: string; label: string }): string {
  return trOr(`connection.form.field.${f.key.replace(/\./g, '_')}`, f.label)
}
function fieldHelp(f: { key: string; help?: string }): string {
  return f.help ? trOr(`connection.form.help.${f.key.replace(/\./g, '_')}`, f.help) : ''
}

const props = defineProps<{
  driver?: DriverInfo | null
  initial?: ConnectionProfile | null
}>()
const emit = defineEmits<{
  (e: 'saved', profile: ConnectionProfile): void
  (e: 'cancel'): void
}>()

const store = useConnectionsStore()
const message = useMessage()

const driverList = computed(() => store.drivers)
const driverLocked = computed(() => !!props.initial)

function pickInitialDriver(): DriverInfo | null {
  if (props.driver) return props.driver
  if (props.initial) {
    const d = driverList.value.find((dd) => dd.name === props.initial!.driver)
    if (d) return d
  }
  return driverList.value[0] ?? null
}

const selectedDriver = ref<DriverInfo | null>(pickInitialDriver())

// Drivers may arrive after the form mounts (refreshDrivers is async). Promote
// the first available driver into selection once they show up.
watch(
  driverList,
  (list) => {
    if (!selectedDriver.value && list.length) {
      selectedDriver.value = pickInitialDriver()
    }
  },
  { immediate: true },
)

function selectDriver(d: DriverInfo) {
  if (driverLocked.value) return
  if (selectedDriver.value?.name === d.name) return
  selectedDriver.value = d
}

const name = ref<string>(props.initial?.name ?? '')
// Group picker: simple dropdown bound to the group id. New groups are
// created from the sidebar's right-click menu (新建分组) — keeping the
// concerns separate avoids cluttering the connection form with group CRUD.
const groupId = ref<string | null>(props.initial?.groupId ?? null)

// Walk dotted-key segments. Returns undefined when the path is unset.
function getPath(obj: any, path: string): any {
  return path.split('.').reduce((acc, key) => (acc == null ? acc : acc[key]), obj)
}
function setPath(obj: any, path: string, value: any) {
  const parts = path.split('.')
  let cur = obj
  for (let i = 0; i < parts.length - 1; i++) {
    const k = parts[i]
    if (cur[k] == null || typeof cur[k] !== 'object') cur[k] = {}
    cur = cur[k]
  }
  cur[parts[parts.length - 1]] = value
}

// Build the initial values object from defaults + initial profile.
function buildInitialValues(): Record<string, any> {
  const v: Record<string, any> = {
    ssl: {},
    sshTunnel: {},
    params: {},
  }
  const drv = selectedDriver.value
  if (!drv) return v
  for (const f of drv.schema) {
    let val: any = f.default ?? ''
    if (f.type === 'number') val = f.default ? Number(f.default) : 0
    if (f.type === 'bool') val = false
    setPath(v, f.key, val)
  }
  // Override with the persisted profile (no secrets — keyring is opaque). Only
  // seed when the active driver matches the saved profile; if the user has
  // swapped drivers (locked editing forbids this) we'd otherwise leak fields
  // from a foreign schema.
  if (props.initial && props.initial.driver === drv.name) {
    if (props.initial.host !== undefined) v.host = props.initial.host
    if (props.initial.port !== undefined) v.port = props.initial.port
    if (props.initial.user !== undefined) v.user = props.initial.user
    if (props.initial.database !== undefined) v.database = props.initial.database
    if (props.initial.params) v.params = { ...props.initial.params }
    if (props.initial.ssl) v.ssl = { ...props.initial.ssl }
    if (props.initial.sshTunnel) v.sshTunnel = { ...props.initial.sshTunnel }
  }
  return v
}

const values = ref<Record<string, any>>(buildInitialValues())
watch(selectedDriver, () => {
  values.value = buildInitialValues()
})

// Group fields by their declared group. Groups are stable keys from the driver
// (general → advanced → ssl → ssh); the display labels are localized in the
// template. Driver-specific buckets land after the known ones (alphabetical).
const GROUP_ORDER = ['general', 'advanced', 'ssl', 'ssh']
type SchemaField = NonNullable<DriverInfo['schema']>[number]
const grouped = computed(() => {
  const groups = new Map<string, SchemaField[]>()
  const drv = selectedDriver.value
  if (!drv) return []
  for (const f of drv.schema) {
    const g = f.group || 'general'
    if (!groups.has(g)) groups.set(g, [])
    groups.get(g)!.push(f)
  }
  const entries = Array.from(groups.entries())
  entries.sort((a, b) => {
    const ai = GROUP_ORDER.indexOf(a[0])
    const bi = GROUP_ORDER.indexOf(b[0])
    if (ai === -1 && bi === -1) return a[0].localeCompare(b[0])
    if (ai === -1) return 1
    if (bi === -1) return -1
    return ai - bi
  })
  return entries
})

// Segmented-control selected group. Defaults to the first group when the
// driver changes — never persists across drivers since the field set is
// different.
const activeGroup = ref<string>('')
watch(
  grouped,
  (gs) => {
    if (!gs.length) {
      activeGroup.value = ''
      return
    }
    if (!gs.some(([g]) => g === activeGroup.value)) {
      activeGroup.value = gs[0][0]
    }
  },
  { immediate: true },
)

function buildDraft(): ConnectionDraft {
  // Pull values back out into the binding shape.
  const v = values.value
  const draft: ConnectionDraft = {
    id: props.initial?.id,
    name: name.value.trim(),
    driver: selectedDriver.value?.name ?? '',
    groupId: groupId.value ?? undefined,
    host: v.host ?? '',
    port: v.port != null && v.port !== '' ? Number(v.port) : 0,
    user: v.user ?? '',
    database: v.database || undefined,
    params: pruneParams(v.params),
    ssl: hasSSL(v.ssl) ? v.ssl : undefined,
    sshTunnel: hasSSH(v.sshTunnel) ? cleanSSHForDraft(v.sshTunnel) : undefined,
    password: v.password || undefined,
    sshPassword: v.sshTunnel?.password || undefined,
    sshKeyPassword: v.sshTunnel?.privateKeyPass || undefined,
  }
  return draft
}

function pruneParams(p: Record<string, any> | undefined) {
  if (!p) return undefined
  const out: Record<string, string> = {}
  for (const [k, val] of Object.entries(p)) {
    if (val !== '' && val != null) out[k] = String(val)
  }
  return Object.keys(out).length ? out : undefined
}
function hasSSL(s: any): boolean {
  return s && s.mode && s.mode !== 'disable'
}
function hasSSH(s: any): boolean {
  return s && (s.host || s.user)
}
function cleanSSHForDraft(s: any): any {
  // Strip the secret fields — they ride on the top-level draft.* keys instead.
  const out = { ...s }
  delete out.password
  delete out.privateKeyPass
  return out
}

// Test-connection result is rendered inline in the action bar's status strip
// rather than as a toast — that keeps the user's eyes on the form they were
// just editing and survives across redraws (toasts vanish after ~3 s).
type TestStatus = 'idle' | 'running' | 'success' | 'error' | 'canceled'
const testStatus = ref<TestStatus>('idle')
const testMessage = ref<string>('')
const testElapsedMs = ref<number>(0)
const testCtrl = ref<AbortController | null>(null)

const testing = computed(() => testStatus.value === 'running')

async function onTest() {
  if (testing.value) return
  testStatus.value = 'running'
  testMessage.value = t('connection.form.testing')
  testElapsedMs.value = 0
  testCtrl.value = new AbortController()
  const start = Date.now()
  try {
    await store.test(buildDraft(), testCtrl.value.signal)
    testElapsedMs.value = Date.now() - start
    testStatus.value = 'success'
    testMessage.value = t('connection.form.testSuccess')
  } catch (e: any) {
    testElapsedMs.value = Date.now() - start
    if (testCtrl.value?.signal.aborted) {
      testStatus.value = 'canceled'
      testMessage.value = t('connection.form.testCanceled')
    } else {
      testStatus.value = 'error'
      testMessage.value = t('common.connectFailed', { error: formatErr(e) })
    }
  } finally {
    testCtrl.value = null
  }
}
function cancelTest() {
  testCtrl.value?.abort()
}
function clearTestResult() {
  if (testing.value) return
  testStatus.value = 'idle'
  testMessage.value = ''
  testElapsedMs.value = 0
}

// Wipe stale results the moment the user edits anything — a green "连接成功"
// hanging around after they changed the host is misleading.
watch(values, () => { clearTestResult() }, { deep: true })
watch(name, () => { clearTestResult() })

const saving = ref(false)
async function onSave() {
  if (!name.value.trim()) {
    message.warning(t('connection.form.nameRequired'))
    return
  }
  if (!selectedDriver.value) {
    message.warning(t('connection.form.driverRequired'))
    return
  }
  saving.value = true
  try {
    const saved = await store.save(buildDraft())
    message.success(t('common.saved'))
    emit('saved', saved)
  } catch (e: any) {
    message.error(t('common.saveFailed', { error: formatErr(e) }))
  } finally {
    saving.value = false
  }
}

function formatErr(e: any): string {
  if (!e) return 'unknown'
  if (e instanceof Error) {
    // Wails v3 serialises Go errors as JSON + wraps them in new Error(text).
    // Try to unpack the meaningful parts.
    try {
      const parsed = JSON.parse(e.message)
      if (parsed.message) {
        let msg: string = parsed.message
        // Strip the generic Wails wrapper prefix.
        msg = msg.replace(/^Bound method returned an error:\s*/, '')
        if (parsed.cause) {
          const cause = typeof parsed.cause === 'string' ? parsed.cause : JSON.stringify(parsed.cause)
          if (cause !== msg) msg += '\n' + cause
        }
        return msg
      }
    } catch { /* not a Wails CallError JSON — fall through */ }
    return e.message
  }
  return String(e)
}

function selectOptions(opts: string[]) {
  return opts.map((o) => ({ label: o, value: o }))
}
</script>

<template>
  <div class="form">
    <!-- Two-column layout. Left rail = driver-type picker, spans the full
         window height. Right column = connection-info pane (scrollable) +
         action bar pinned to the column's bottom. -->
    <!-- Driver-type rail. Locked when editing — a saved profile's driver
         can't be swapped without orphaning keyring credentials. -->
    <aside class="driver-rail">
      <div class="rail-label">{{ $t('connection.form.driverType') }}</div>
      <div class="rail-list">
        <button
          v-for="d in driverList"
          :key="d.name"
          type="button"
          class="rail-item mono"
          :class="{
            active: selectedDriver?.name === d.name,
            locked: driverLocked && selectedDriver?.name !== d.name,
          }"
          :disabled="driverLocked && selectedDriver?.name !== d.name"
          @click="selectDriver(d)"
        >
          <span class="rail-dot" :class="{ active: selectedDriver?.name === d.name }" />
          <span class="rail-name">{{ d.name }}</span>
        </button>
        <div v-if="driverList.length === 0" class="rail-empty">{{ $t('connection.form.noDrivers') }}</div>
      </div>
    </aside>

    <!-- Right column: scrollable connection-info pane on top, action bar
         pinned to the column's own bottom. -->
    <div class="form-right">
      <!-- Right pane: header + tabs + active group fields. -->
      <div class="form-pane">
      <!-- Header: name (wide) + group (narrow) inline. label-left keeps the
           pattern consistent with the field rows below. -->
      <n-form
        label-placement="left"
        label-width="64px"
        require-mark-placement="right-hanging"
        size="small"
        class="header-form"
      >
        <div class="header-row">
          <n-form-item :label="$t('connection.form.name')" required class="header-item header-item-grow">
            <n-input v-model:value="name" size="small" placeholder="My MySQL" />
          </n-form-item>
          <n-form-item :label="$t('connection.form.group')" class="header-item header-item-group">
            <!-- Native HTML <select> — the system's own dropdown chrome
                 (caret, popup) reads as a real desktop control instead of a
                 Web overlay (UI_SPEC.md "向原生靠拢"). The empty option acts
                 as the "未分组" clearable choice. -->
            <select
              v-model="groupId"
              class="group-select"
            >
              <option :value="null">{{ $t('connection.form.ungrouped') }}</option>
              <option v-for="g in store.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
            </select>
          </n-form-item>
        </div>
      </n-form>

      <!-- Segmented control: centered in the window via the rail container. -->
      <div class="tabs-wrap">
        <n-tabs
          v-model:value="activeGroup"
          type="segment"
          size="small"
          animated
          class="group-tabs"
        >
          <n-tab-pane
            v-for="[g, fields] in grouped"
            :key="g"
            :name="g"
            :tab="groupLabel(g)"
            display-directive="show:lazy"
          >
            <n-form
              label-placement="left"
              label-width="96px"
              require-mark-placement="right-hanging"
              size="small"
              class="pane-form"
            >
              <n-form-item
                v-for="f in fields"
                :key="f.key"
                :label="fieldLabel(f)"
                :required="f.required"
                :show-feedback="!!f.help"
              >
                <template v-if="f.type === 'select'">
                  <n-select
                    :value="getPath(values, f.key)"
                    :options="selectOptions(f.options ?? [])"
                    size="small"
                    @update:value="setPath(values, f.key, $event)"
                  />
                </template>
                <template v-else-if="f.type === 'number'">
                  <n-input-number
                    :value="getPath(values, f.key)"
                    size="small"
                    :min="0"
                    :show-button="false"
                    @update:value="setPath(values, f.key, $event)"
                  />
                </template>
                <template v-else-if="f.type === 'bool'">
                  <n-checkbox
                    :checked="!!getPath(values, f.key)"
                    @update:checked="setPath(values, f.key, $event)"
                  />
                </template>
                <template v-else-if="f.type === 'password'">
                  <n-input
                    :value="getPath(values, f.key) ?? ''"
                    type="password"
                    show-password-on="click"
                    size="small"
                    @update:value="setPath(values, f.key, $event)"
                  />
                </template>
                <template v-else>
                  <n-input
                    :value="getPath(values, f.key) ?? ''"
                    size="small"
                    @update:value="setPath(values, f.key, $event)"
                  />
                </template>
                <template v-if="f.help" #feedback>
                  <span class="hint">{{ fieldHelp(f) }}</span>
                </template>
              </n-form-item>
            </n-form>
          </n-tab-pane>
        </n-tabs>
      </div>
      </div>

      <!-- Right-column action bar. Two rows:
           * status strip: replaces the test-result toast — only shown when
             a test is in flight, succeeded, failed, or was cancelled.
           * buttons: 关闭 / 测试连接 / 保存 -->
      <footer class="action-bar" :class="{ 'has-status': testStatus !== 'idle' }">
      <div
        v-if="testStatus !== 'idle'"
        class="status-strip"
        :class="`status-${testStatus}`"
      >
        <span class="status-dot" />
        <span class="status-text">{{ testMessage }}</span>
        <span v-if="testStatus === 'success' && testElapsedMs > 0" class="status-meta mono">
          {{ testElapsedMs }} ms
        </span>
        <span class="status-spacer" />
        <button
          v-if="testStatus !== 'running'"
          class="status-dismiss"
          type="button"
          :aria-label="$t('common.close')"
          @click="clearTestResult"
        >×</button>
      </div>

      <div class="actions">
        <!-- All buttons clustered on the right. Order reads
             关闭 → 测试连接 → 保存 so the primary 保存 keeps the
             rightmost (default-action) slot. -->
        <div class="actions-right">
          <n-button v-if="testing" size="small" @click="cancelTest">{{ $t('connection.form.cancelTest') }}</n-button>
          <n-button v-else size="small" @click="onTest" :loading="testing">{{ $t('connection.form.testConn') }}</n-button>
          <n-button size="small" @click="emit('cancel')">{{ $t('common.close') }}</n-button>
          <n-button size="small" type="primary" :loading="saving" @click="onSave">{{ $t('common.save') }}</n-button>
        </div>
      </div>
    </footer>
    </div>
  </div>
</template>

<style scoped>
/* Form occupies the entire window body; the parent provides height via flex.
   Outer layout is a horizontal split: driver-rail (fixed) + right column
   (1fr). The right column owns its own scrolling pane + action bar so the
   rail extends from window top to window bottom. */
.form {
  min-width: 0;
  min-height: 0;
  height: 100%;
  overflow: hidden;
  display: flex;
  flex-direction: row;
}
/* Right column: scrollable connection-info pane stacked on top of the
   action bar. Grid keeps the action bar pinned to the column's bottom
   regardless of pane content height. */
.form-right {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: grid;
  grid-template-rows: 1fr auto;
}
.form-pane {
  min-width: 0;
  min-height: 0;
  overflow: auto;
  padding-top: 6px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.hint { font-size: 11px; opacity: 0.65; }

/* --- Driver-type rail (left) -------------------------------------------
   Compact desktop list. Active item gets a soft highlight + green dot.
   Locked items (editing mode, foreign drivers) dim and disable. */
.driver-rail {
  flex: 0 0 132px;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--n-border-color, rgba(127,127,127,0.15));
  padding: 16px 6px 16px 16px;
  gap: 4px;
}
.rail-label {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  opacity: 0.55;
  padding: 0 6px 4px;
}
.rail-list {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.rail-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 8px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font-size: 12px;
  text-align: left;
  cursor: default;
  width: 100%;
  transition: background 80ms ease;
}
.rail-item:hover:not(:disabled) {
  background: rgba(127, 127, 127, 0.1);
}
.rail-item.active {
  background: rgba(24, 160, 88, 0.12);
  color: inherit;
  font-weight: 600;
}
.rail-item.locked,
.rail-item:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
.rail-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex: 0 0 auto;
  background: rgba(127, 127, 127, 0.35);
}
.rail-dot.active { background: #18a058; }
.rail-name { flex: 1 1 auto; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rail-empty {
  padding: 6px 8px;
  font-size: 11px;
  opacity: 0.5;
}

/* --- Header (name + group, inline) --------------------------------------
   Two form-items share a single row. Name takes the elastic share; group
   sits on a fixed track wide enough for typical group names. Removing the
   default form-item feedback line collapses the wasted vertical strip below
   the inputs that we don't use here. */
.header-form {
  padding-bottom: 4px;
  border-bottom: 1px solid var(--n-border-color, rgba(127,127,127,0.15));
}
.header-row {
  display: flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
  padding: 0 16px;
}
.header-item { margin-bottom: 0 !important; min-width: 0; }
.header-item-grow { flex: 1 1 auto; }
.header-item-group { flex: 0 0 220px; }
.header-form :deep(.n-form-item-feedback-wrapper) { min-height: 0; padding: 0; }

/* Native <select> for the group picker — sized to align with Naive's small
   input (28px) so the header row reads as one band. We keep the system
   caret (no -webkit-appearance: none) since the whole point of going native
   here is to expose the OS-drawn dropdown chrome. */
.group-select {
  width: 100%;
  height: 28px;
  padding: 0 8px;
  font: inherit;
  font-size: 13px;
  color: inherit;
  background: var(--n-color, transparent);
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.3));
  border-radius: 3px;
  outline: none;
  box-sizing: border-box;
  transition: border-color 120ms ease, box-shadow 120ms ease;
}
.group-select:hover {
  border-color: var(--n-border-color-hover, rgba(127, 127, 127, 0.5));
}
.group-select:focus {
  border-color: var(--n-border-color-focus, #18a058);
  box-shadow: 0 0 0 2px rgba(24, 160, 88, 0.18);
}

/* --- Segmented control (liquid glass) -----------------------------------
   Replaces Naive UI's default segment styling with a frosted-glass look
   that matches the sidebar toggle in AppShell.vue. The rail gets a
   translucent gradient + backdrop blur + specular edge; the active pill is
   a brighter, more opaque glass layer. */
.tabs-wrap { display: flex; flex-direction: column; min-width: 0; padding: 0 16px}
.group-tabs :deep(.n-tabs) {
  min-width: 0;
  overflow: hidden;
}
.group-tabs :deep(.n-tabs-nav) {
  display: flex;
  justify-content: center;
}
.group-tabs :deep(.n-tabs-rail) {
  min-width: 0;
  margin: 0 auto;
  padding: 3px;
  border-radius: 8px;
  background:
    linear-gradient(180deg,
      rgba(255, 255, 255, 0.5) 0%,
      rgba(255, 255, 255, 0.18) 100%);
  backdrop-filter: blur(18px) saturate(180%);
  -webkit-backdrop-filter: blur(18px) saturate(180%);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.75),
    inset 0 -1px 0 rgba(0, 0, 0, 0.04),
    0 0 0 0.5px rgba(0, 0, 0, 0.1),
    0 1px 2px rgba(0, 0, 0, 0.08);
}
.group-tabs :deep(.n-tabs-tab) {
  padding: 3px 16px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 500;
  color: inherit;
  opacity: 0.7;
  transition: opacity 120ms ease, background 120ms ease;
}
.group-tabs :deep(.n-tabs-tab:hover) {
  opacity: 0.95;
  background: rgba(255, 255, 255, 0.35);
}
.group-tabs :deep(.n-tabs-tab--active) {
  opacity: 1;
  font-weight: 600;
  color: inherit;
  background:
    linear-gradient(180deg,
      rgba(255, 255, 255, 0.85) 0%,
      rgba(255, 255, 255, 0.55) 100%);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.95),
    inset 0 -1px 0 rgba(0, 0, 0, 0.04),
    0 0.5px 1px rgba(0, 0, 0, 0.08);
}
.group-tabs :deep(.n-tab-pane) {
  padding-top: 12px;
  min-width: 0;
  overflow: auto;
}
.group-tabs :deep(.n-tabs-pane-wrapper) {
  min-width: 0;
}

@media (prefers-color-scheme: dark) {
  .group-tabs :deep(.n-tabs-rail) {
    background:
      linear-gradient(180deg,
        rgba(255, 255, 255, 0.12) 0%,
        rgba(255, 255, 255, 0.04) 100%);
    box-shadow:
      inset 0 1px 0 rgba(255, 255, 255, 0.18),
      inset 0 -1px 0 rgba(0, 0, 0, 0.3),
      0 0 0 0.5px rgba(255, 255, 255, 0.06),
      0 1px 2px rgba(0, 0, 0, 0.3);
  }
  .group-tabs :deep(.n-tabs-tab:hover) {
    background: rgba(255, 255, 255, 0.1);
  }
  .group-tabs :deep(.n-tabs-tab--active) {
    background:
      linear-gradient(180deg,
        rgba(255, 255, 255, 0.2) 0%,
        rgba(255, 255, 255, 0.08) 100%);
    box-shadow:
      inset 0 1px 0 rgba(255, 255, 255, 0.25),
      inset 0 -1px 0 rgba(0, 0, 0, 0.25),
      0 0.5px 1px rgba(0, 0, 0, 0.25);
  }
}

@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  .group-tabs :deep(.n-tabs-rail) { background: rgba(255, 255, 255, 0.6); }
  @media (prefers-color-scheme: dark) {
    .group-tabs :deep(.n-tabs-rail) { background: rgba(255, 255, 255, 0.1); }
  }
}

/* --- Field rows --------------------------------------------------------- */
.pane-form { min-width: 0; }
.pane-form :deep(.n-form-item) { margin-bottom: 8px; }
/* When show-feedback is false the wrapper still reserves space — collapse it. */
.pane-form :deep(.n-form-item-feedback-wrapper:empty) { min-height: 0; padding: 0; }
.pane-form :deep(.n-form-item-label) {
  font-size: 12px;
  opacity: 0.85;
}

/* --- Action bar --------------------------------------------------------
   Sticky at the window bottom. Status strip (optional) on top, then the
   button row. All buttons cluster at the right
   (关闭 / 测试连接 / 保存) so 保存 keeps the default-action position. */
.action-bar {
  border-top: 1px solid var(--n-border-color);
  background: var(--n-color, transparent);
}
.actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  padding: 8px 18px;
}
.actions-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* --- Status strip ------------------------------------------------------ */
.status-strip {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 18px;
  font-size: 12px;
  border-bottom: 1px solid var(--n-border-color, rgba(127,127,127,0.12));
  background: rgba(127, 127, 127, 0.04);
}
.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: 0 0 auto;
  background: currentColor;
}
.status-text {
  flex: 1 1 auto;
  min-width: 0;
  word-break: break-word;
  white-space: pre-wrap;
  user-select: text;
  -webkit-user-select: text;
}
.status-meta {
  opacity: 0.55;
  font-size: 11px;
}
.status-spacer { flex: 1 1 auto; }
.status-dismiss {
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font-size: 14px;
  line-height: 1;
  opacity: 0.5;
}
.status-dismiss:hover { background: rgba(127, 127, 127, 0.15); opacity: 0.9; }

.status-running { color: var(--n-info-color, #2080f0); }
.status-success { color: var(--n-success-color, #18a058); }
.status-error   { color: var(--n-error-color, #d03050); }
.status-canceled { color: var(--n-warning-color, #f0a020); }

.status-running .status-dot {
  /* Pulse while the request is in flight so the user knows it isn't stuck. */
  animation: statusPulse 1.1s ease-in-out infinite;
}
@keyframes statusPulse {
  0%, 100% { transform: scale(1); opacity: 1; }
  50% { transform: scale(1.4); opacity: 0.5; }
}

.mono {
  font-family: ui-monospace, "SF Mono", "JetBrains Mono", Menlo, Consolas, monospace;
}
</style>
