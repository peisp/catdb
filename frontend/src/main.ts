import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { installGridContextMenuListener } from './api/gridContextMenu'
import { installTabContextMenuListener } from './api/tabContextMenu'
import './styles/global.css'

const app = createApp(App)
app.use(createPinia())
// Subscribe once to Wails native context-menu actions.
installGridContextMenuListener()
installTabContextMenuListener()
app.mount('#app')
