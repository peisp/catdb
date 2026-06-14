// Event helpers — wrap @wailsio/runtime so components only see typed callbacks.
//
// The native Wails Event payload carries name + data + sender; the front-end
// rarely cares about anything other than `data`, so the wrapper unwraps it.
import { Events } from '@wailsio/runtime'

/** Subscribe to a Wails event. Returns the unsubscribe function. */
export function on<T = unknown>(name: string, cb: (data: T) => void): () => void {
  return Events.On(name, (evt) => cb(evt.data as T))
}

/** Subscribe once. The handler is removed after the first invocation. */
export function once<T = unknown>(name: string, cb: (data: T) => void): () => void {
  return Events.Once(name, (evt) => cb(evt.data as T))
}
