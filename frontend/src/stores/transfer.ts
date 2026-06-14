// stores/transfer — central registry of in-flight transfers (export/import).
// Components subscribe to push updates via Pinia rather than each managing
// their own progress event listener.
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { transfer as transferApi } from '../api'
import type { TransferProgress } from '../api/transfer'

export interface TransferState {
  id: string
  kind: 'export' | 'import'
  label: string
  rows: number
  done: boolean
  error: string | null
}

export const useTransferStore = defineStore('transfer', () => {
  const items = ref<Record<string, TransferState>>({})

  let off: (() => void) | null = null
  function ensureListener() {
    if (off) return
    off = transferApi.onProgress((p: TransferProgress) => {
      const existing = items.value[p.transferId]
      if (!existing) return
      items.value = {
        ...items.value,
        [p.transferId]: {
          ...existing,
          rows: p.rows,
          done: p.done,
          error: p.error ?? null,
        },
      }
    })
  }

  function register(id: string, kind: 'export' | 'import', label: string) {
    ensureListener()
    items.value = {
      ...items.value,
      [id]: { id, kind, label, rows: 0, done: false, error: null },
    }
  }

  function remove(id: string) {
    const next = { ...items.value }
    delete next[id]
    items.value = next
  }

  return { items, register, remove }
})
