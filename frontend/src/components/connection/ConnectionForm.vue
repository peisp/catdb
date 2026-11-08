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
  CButton,
  CCheckbox,
  CInput,
  CInputNumber,
  CSelect,
  CSpin,
  CTabPane,
  CTabs,
  useMessage,
} from '../ui'
import type { ConnectionDraft, ConnectionProfile, DriverInfo } from '../../api/connections'
import { useConnectionsStore } from '../../stores/connections'
import { t, i18n } from '../../i18n'
import AppIcon from '../shared/AppIcon.vue'
import { driverLogo } from '../../assets/logo'

// Localize a driver-provided schema string (group/label/help) by key, falling
// back to the driver's own (English baseline) text when no translation exists.
// Keeps the driver locale-agnostic while still localizing the known fields.
// Field lookups try a driver-scoped key first (`<driver>_<key>`) so drivers
// can give shared keys their own wording (SQLite's "database" is a file path).
function trOr(keys: string[], fallback: string): string {
  for (const k of keys) {
    if (i18n.global.te(k)) return i18n.global.t(k) as string
  }
  return fallback
}
function groupLabel(g: string): string {
  return trOr([`connection.form.groups.${g}`], g)
}
function fieldLabel(f: { key: string; label: string }): string {
  const k = f.key.replace(/\./g, '_')
  const drv = selectedDriver.value?.name
  return trOr(
    drv ? [`connection.form.field.${drv}_${k}`, `connection.form.field.${k}`] : [`connection.form.field.${k}`],
    f.label,
  )
}
function fieldHelp(f: { key: string; help?: string }): string {
  if (!f.help) return ''
  const k = f.key.replace(/\./g, '_')
  const drv = selectedDriver.value?.name
  return trOr(
    drv ? [`connection.form.help.${drv}_${k}`, `connection.form.help.${k}`] : [`connection.form.help.${k}`],
    f.help,
  )
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

// Display order for the driver rail: mainstream databases first, then the
// rest in registry (alphabetical) order.
const DRIVER_ORDER = ['mysql', 'mariadb', 'postgres', 'sqlite']
const driverList = computed(() => {
  const rank = (n: string) => {
    const i = DRIVER_ORDER.indexOf(n)
    return i === -1 ? DRIVER_ORDER.length : i
  }
  return [...store.drivers].sort((a, b) => rank(a.name) - rank(b.name))
})
const driverLocked = computed(() => !!props.initial)
// Editing an existing profile: password fields are write-only (secrets live
// in the OS keychain and never round-trip to the UI) — blank keeps the
// stored secret, so show a hint plus an explicit "clear" affordance.
const isEditing = computed(() => !!props.initial?.id)

// Secret fields the user explicitly asked to clear on save (schema keys:
// password / sshTunnel.password / sshTunnel.privateKeyPass). Typing a new
// value into the field withdraws the pending clear.
const clearedSecrets = ref(new Set<string>())
function isCleared(key: string): boolean {
  return clearedSecrets.value.has(key)
}
function toggleClear(key: string) {
  const next = new Set(clearedSecrets.value)
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
    setPath(values.value, key, '')
  }
  clearedSecrets.value = next
}
function onSecretInput(key: string, v: string) {
  setPath(values.value, key, v)
  if (v && clearedSecrets.value.has(key)) {
    const next = new Set(clearedSecrets.value)
    next.delete(key)
    clearedSecrets.value = next
  }
}

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
// Environment label (闸 1, AGENT_DESIGN §5). Stored value is the raw English
// key ('' | dev | test | staging | prod); labels are localized. '' = unmarked.
const environment = ref<string>(props.initial?.environment ?? '')
const ENVIRONMENTS = ['', 'dev', 'test', 'staging', 'prod'] as const
const environmentOptions = computed(() =>
  ENVIRONMENTS.map((e) => ({
    value: e,
    label: e ? t(`connection.form.environments.${e}`) : t('connection.form.environments.unmarked'),
  })),
)

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
    if (f.type === 'bool') val = f.default === 'true'
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
    environment: environment.value || undefined,
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
    clearPassword: isCleared('password') || undefined,
    clearSSHPassword: isCleared('sshTunnel.password') || undefined,
    clearSSHKeyPassword: isCleared('sshTunnel.privateKeyPass') || undefined,
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
          <AppIcon :src="driverLogo(d.name)" :size="14" />
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
      <!-- Header: name (wide) + group (narrow) inline. Labels sit left of the
           control, matching the field rows below. -->
      <div class="header-form">
        <div class="header-row">
          <div class="field field-inline header-item header-item-grow">
            <span class="field-label">{{ $t('connection.form.name') }}<i class="req">*</i></span>
            <div class="field-ctl">
              <CInput v-model="name" size="small" placeholder="My DataBase" />
            </div>
          </div>
          <div class="field field-inline header-item header-item-group">
            <span class="field-label">{{ $t('connection.form.group') }}</span>
            <div class="field-ctl">
              <!-- Native HTML <select> — the system's own dropdown chrome
                   (caret, popup) reads as a real desktop control instead of a
                   Web overlay (DESIGN.md "向原生靠拢"). The empty option acts
                   as the "未分组" clearable choice. -->
              <select
                v-model="groupId"
                class="group-select"
              >
                <option :value="null">{{ $t('connection.form.ungrouped') }}</option>
                <option v-for="g in store.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
              </select>
            </div>
          </div>
          <!-- Environment label (闸 1). Same native <select> chrome as the
               group picker; raw English key is stored, label is localized. -->
          <div class="field field-inline header-item header-item-env">
            <span class="field-label field-label-wide">{{ $t('connection.form.environment') }}</span>
            <div class="field-ctl">
              <select v-model="environment" class="group-select">
                <option v-for="o in environmentOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
              </select>
            </div>
          </div>
        </div>
      </div>

      <!-- Segmented control: centered in the window via the rail container.
           Keyed by driver: the segment capsule only re-measures on value
           change, so a driver switch that keeps the same activeGroup would
           leave a stale-sized capsule overlapping the new tab layout. -->
      <div class="tabs-wrap">
        <CTabs
          :key="selectedDriver?.name ?? ''"
          v-model:value="activeGroup"
          class="group-tabs"
        >
          <CTabPane
            v-for="[g, fields] in grouped"
            :key="g"
            :name="g"
            :tab="groupLabel(g)"
          >
            <div class="pane-form">
              <div v-for="f in fields" :key="f.key" class="field field-inline">
                <span class="field-label field-label-wide">
                  {{ fieldLabel(f) }}<i v-if="f.required" class="req">*</i>
                </span>
                <div class="field-ctl">
                  <CSelect
                    v-if="f.type === 'select'"
                    :value="getPath(values, f.key)"
                    :options="selectOptions(f.options ?? [])"
                    size="small"
                    @update:value="setPath(values, f.key, $event)"
                  />
                  <CInputNumber
                    v-else-if="f.type === 'number'"
                    :value="getPath(values, f.key)"
                    size="small"
                    :min="0"
                    @update:value="setPath(values, f.key, $event)"
                  />
                  <CCheckbox
                    v-else-if="f.type === 'bool'"
                    :model-value="!!getPath(values, f.key)"
                    @update:model-value="setPath(values, f.key, $event)"
                  />
                  <CInput
                    v-else-if="f.type === 'password'"
                    :model-value="getPath(values, f.key) ?? ''"
                    type="password"
                    size="small"
                    @update:model-value="onSecretInput(f.key, $event)"
                  />
                  <CInput
                    v-else
                    :model-value="getPath(values, f.key) ?? ''"
                    size="small"
                    @update:model-value="setPath(values, f.key, $event)"
                  />
                  <div v-if="f.help || (f.type === 'password' && isEditing)" class="field-feedback">
                    <span v-if="f.type === 'password' && isEditing" class="hint">
                      {{ isCleared(f.key) ? $t('connection.form.passwordWillClear') : $t('connection.form.passwordKeepHint') }}
                      <button type="button" class="hint-link" @click="toggleClear(f.key)">
                        {{ isCleared(f.key) ? $t('connection.form.passwordClearUndo') : $t('connection.form.passwordClear') }}
                      </button>
                    </span>
                    <span v-else class="hint">{{ fieldHelp(f) }}</span>
                  </div>
                </div>
              </div>
            </div>
          </CTabPane>
        </CTabs>
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
          <CButton v-if="testing" size="small" @click="cancelTest">{{ $t('connection.form.cancelTest') }}</CButton>
          <CButton v-else size="small" @click="onTest">{{ $t('connection.form.testConn') }}</CButton>
          <CButton size="small" @click="emit('cancel')">{{ $t('common.close') }}</CButton>
          <CButton size="small" variant="primary" :disabled="saving" @click="onSave">
            <CSpin v-if="saving" :size="12" />
            {{ $t('common.save') }}
          </CButton>
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
.hint { font-size: var(--catdb-fs-mini); opacity: 0.65; }
/* Inline "clear saved password" affordance inside a field hint. */
.hint-link {
  border: none;
  background: transparent;
  padding: 0;
  margin-left: 4px;
  font: inherit;
  color: var(--catdb-accent);
  cursor: pointer;
}
.hint-link:hover { text-decoration: underline; }

/* --- Driver-type rail (left) -------------------------------------------
   Compact desktop list. Active item gets a soft highlight + green dot.
   Locked items (editing mode, foreign drivers) dim and disable. */
.driver-rail {
  flex: 0 0 132px;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--catdb-separator);
  padding: 16px 6px 16px 16px;
  gap: 4px;
}
.rail-label {
  font-size: var(--catdb-fs-mini);
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
  border-radius: var(--catdb-rounded-xs);
  background: transparent;
  color: inherit;
  font-size: var(--catdb-fs-small);
  text-align: left;
  cursor: default;
  width: 100%;
  transition: background 80ms ease;
}
.rail-item:hover:not(:disabled) {
  background: var(--catdb-hover-fill);
}
.rail-item.active {
  background: var(--catdb-accent-soft);
  color: inherit;
  font-weight: 600;
}
.rail-item.locked,
.rail-item:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
.rail-name { flex: 1 1 auto; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rail-empty {
  padding: 6px 8px;
  font-size: var(--catdb-fs-mini);
  opacity: 0.5;
}

/* --- Header (name + group, inline) --------------------------------------
   Two form-items share a single row. Name takes the elastic share; group
   sits on a fixed track wide enough for typical group names. Removing the
   default form-item feedback line collapses the wasted vertical strip below
   the inputs that we don't use here. */
.header-form {
  padding-bottom: 4px;
  border-bottom: 1px solid var(--catdb-separator);
}
.header-row {
  display: flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
  padding: 0 16px;
}
.header-item { min-width: 0; }
.header-item-grow { flex: 1 1 auto; }
.header-item-group { flex: 0 0 180px; }
.header-item-env { flex: 0 0 200px; }

/* --- Form rows (label left of control) ---------------------------------- */
.field { min-width: 0; }
.field-inline {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
}
.field-label {
  flex: 0 0 64px;
  padding-top: 4px;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  opacity: 0.85;
  text-align: right;
  white-space: nowrap;
}
.field-label-wide { flex-basis: 96px; }
.req {
  color: var(--catdb-error);
  font-style: normal;
  margin-left: 2px;
}
.field-ctl { flex: 1 1 auto; min-width: 0; }
.field-feedback { padding-top: 2px; }

/* Native <select> for the group picker — sized to match CInput size="small"
   so the header row reads as one band. We keep the system caret (no
   -webkit-appearance: none) since the whole point of going native here is to
   expose the OS-drawn dropdown chrome. */
.group-select {
  width: 100%;
  height: var(--catdb-control-height);
  padding: 0 8px;
  font: inherit;
  font-size: var(--catdb-fs-body);
  color: inherit;
  background: var(--catdb-surface-content);
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  outline: none;
  box-sizing: border-box;
  transition: border-color 120ms ease, box-shadow 120ms ease;
}
.group-select:hover {
  border-color: var(--catdb-control-border);
}
.group-select:focus {
  border-color: var(--catdb-accent);
  box-shadow: var(--catdb-focus-ring);
}

/* --- Segmented control -------------------------------------------------
   CTabs 自带 DESIGN.md 的分段控件样式(轨 + 选中段);这里只补外框布局与
   面板内边距。 */
.tabs-wrap { display: flex; flex-direction: column; min-width: 0; padding: 0 16px}
.group-tabs { min-width: 0; overflow: hidden; }
.group-tabs :deep(.c-tab-pane) {
  min-width: 0;
  overflow: auto;
}

/* --- Field rows --------------------------------------------------------- */
.pane-form {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* --- Action bar --------------------------------------------------------
   Sticky at the window bottom. Status strip (optional) on top, then the
   button row. All buttons cluster at the right
   (关闭 / 测试连接 / 保存) so 保存 keeps the default-action position. */
.action-bar {
  border-top: 1px solid var(--catdb-separator);
  background: transparent;
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
  font-size: var(--catdb-fs-small);
  border-bottom: 1px solid var(--catdb-separator);
  background: var(--catdb-hover-fill);
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
  font-size: var(--catdb-fs-mini);
}
.status-spacer { flex: 1 1 auto; }
.status-dismiss {
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--catdb-rounded-xs);
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font-size: 14px;
  line-height: 1;
  opacity: 0.5;
}
.status-dismiss:hover { background: var(--catdb-hover-fill); opacity: 0.9; }

.status-running { color: var(--catdb-accent); }
.status-success { color: var(--catdb-success); }
.status-error   { color: var(--catdb-error); }
.status-canceled { color: var(--catdb-warning); }

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
