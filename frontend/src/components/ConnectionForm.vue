<script setup lang="ts">
// ConnectionForm — renders connection fields *dynamically* from the driver's
// ConnectionSchema(). Adding a new field on the Go side surfaces here with
// no edits required (CLAUDE.md / ARCHITECTURE.md §3.1).
//
// The form maintains a flat draft object (ConnectionDraft) on top of the
// schema by walking dotted keys: "ssl.mode" → draft.ssl.mode, etc. Secrets
// (.password, sshTunnel.password, sshTunnel.privateKeyPass) are routed to
// the dedicated top-level fields so they hit the keyring path on Save.
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NCheckbox,
  NCollapse,
  NCollapseItem,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSpin,
  useMessage,
} from 'naive-ui'
import type { ConnectionDraft, ConnectionProfile, DriverInfo } from '../api/connections'
import { useConnectionsStore } from '../stores/connections'

const props = defineProps<{
  driver: DriverInfo
  initial?: ConnectionProfile | null
}>()
const emit = defineEmits<{
  (e: 'saved', profile: ConnectionProfile): void
  (e: 'cancel'): void
}>()

const store = useConnectionsStore()
const message = useMessage()

const name = ref<string>(props.initial?.name ?? '')
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
  for (const f of props.driver.schema) {
    let val: any = f.default ?? ''
    if (f.type === 'number') val = f.default ? Number(f.default) : 0
    if (f.type === 'bool') val = false
    setPath(v, f.key, val)
  }
  // Override with the persisted profile (no secrets — keyring is opaque).
  if (props.initial) {
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
watch(
  () => props.driver,
  () => {
    values.value = buildInitialValues()
  },
)

// Group fields by their declared group.
const grouped = computed(() => {
  const groups = new Map<string, typeof props.driver.schema>()
  for (const f of props.driver.schema) {
    const g = f.group || '常规'
    if (!groups.has(g)) groups.set(g, [])
    groups.get(g)!.push(f)
  }
  return Array.from(groups.entries())
})

const groupOptions = computed(() =>
  store.groups.map((g) => ({ label: g.name, value: g.id })),
)

function buildDraft(): ConnectionDraft {
  // Pull values back out into the binding shape.
  const v = values.value
  const draft: ConnectionDraft = {
    id: props.initial?.id,
    name: name.value.trim(),
    driver: props.driver.name,
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

const testing = ref(false)
const testCtrl = ref<AbortController | null>(null)
async function onTest() {
  if (testing.value) return
  testing.value = true
  testCtrl.value = new AbortController()
  try {
    await store.test(buildDraft(), testCtrl.value.signal)
    message.success('连接成功')
  } catch (e: any) {
    message.error(`连接失败: ${formatErr(e)}`)
  } finally {
    testing.value = false
    testCtrl.value = null
  }
}
function cancelTest() {
  testCtrl.value?.abort()
}

const saving = ref(false)
async function onSave() {
  if (!name.value.trim()) {
    message.warning('请填写连接名称')
    return
  }
  saving.value = true
  try {
    const saved = await store.save(buildDraft())
    message.success('已保存')
    emit('saved', saved)
  } catch (e: any) {
    message.error(`保存失败: ${formatErr(e)}`)
  } finally {
    saving.value = false
  }
}

function formatErr(e: any): string {
  if (!e) return 'unknown'
  if (e instanceof Error) return e.message
  return String(e)
}

function selectOptions(opts: string[]) {
  return opts.map((o) => ({ label: o, value: o }))
}
</script>

<template>
  <div class="form">
    <n-form label-placement="left" label-width="120px" require-mark-placement="right-hanging" size="small">
      <n-form-item label="名称" required>
        <n-input v-model:value="name" size="small" placeholder="My MySQL" />
      </n-form-item>
      <n-form-item label="分组">
        <n-select
          v-model:value="groupId"
          :options="groupOptions"
          size="small"
          clearable
          placeholder="未分组"
        />
      </n-form-item>
    </n-form>

    <n-collapse :default-expanded-names="['常规']" arrow-placement="right">
      <n-collapse-item v-for="[g, fields] in grouped" :key="g" :name="g" :title="g">
        <n-form label-placement="left" label-width="160px" size="small">
          <n-form-item
            v-for="f in fields"
            :key="f.key"
            :label="f.label"
            :required="f.required"
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
              <span class="hint">{{ f.help }}</span>
            </template>
          </n-form-item>
        </n-form>
      </n-collapse-item>
    </n-collapse>

    <div class="actions">
      <n-space :size="8">
        <n-button v-if="testing" size="small" @click="cancelTest">取消测试</n-button>
        <n-button v-else size="small" @click="onTest" :loading="testing">测试连接</n-button>
        <n-button size="small" type="primary" :loading="saving" @click="onSave">保存</n-button>
        <n-button size="small" @click="emit('cancel')">关闭</n-button>
      </n-space>
      <n-spin v-if="testing" size="small" class="spin" />
    </div>
  </div>
</template>

<style scoped>
.form { display: flex; flex-direction: column; gap: 12px; }
.actions {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-top: 6px;
  border-top: 1px solid var(--n-border-color);
}
.hint { font-size: 11px; opacity: 0.7; }
.spin { margin-left: 4px; }
</style>
