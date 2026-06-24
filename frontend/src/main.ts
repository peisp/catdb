import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { i18n, isSupportedLocale, setLocale, currentLocale } from './i18n'
import { settings as settingsApi, system as systemApi } from './api'
import { installConnectionContextMenuListener } from './api/connectionContextMenu'
import { installGridContextMenuListener } from './api/gridContextMenu'
import { installSidebarContextMenuListener } from './api/sidebarContextMenu'
import { installTabContextMenuListener } from './api/tabContextMenu'
import { installTableContextMenuListener } from './api/tableContextMenu'
import { installTreeContextMenuListener } from './api/treeContextMenu'
import './styles/global.css'

const app = createApp(App)
app.use(createPinia())
app.use(i18n)
// Subscribe once to Wails native context-menu actions.
installConnectionContextMenuListener()
installGridContextMenuListener()
installSidebarContextMenuListener()
installTabContextMenuListener()
installTableContextMenuListener()
installTreeContextMenuListener()

// Apply the persisted locale (overrides the system-locale default detected in
// src/i18n) and react to the native "View ▸ Language" menu. Wired here, once,
// so every window (main + connection/database editor children) stays in sync.
void settingsApi.getLocale().then((stored) => {
  if (stored && isSupportedLocale(stored)) {
    setLocale(stored)
  } else {
    // First run: persist the system-detected locale so the Go-side native
    // menus (built from app_settings at startup) match the WebView.
    void settingsApi.setLocale(currentLocale())
  }
})
systemApi.onSetLocale((locale) => {
  if (!isSupportedLocale(locale)) return
  setLocale(locale)
  void settingsApi.setLocale(locale)
})

app.mount('#app')
