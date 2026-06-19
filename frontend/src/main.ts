import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { installGridContextMenuListener } from './api/gridContextMenu'
import { installTabContextMenuListener } from './api/tabContextMenu'
import { installTableContextMenuListener } from './api/tableContextMenu'
import { installTreeContextMenuListener } from './api/treeContextMenu'
import './styles/global.css'

const app = createApp(App)
app.use(createPinia())
// Subscribe once to Wails native context-menu actions.
installGridContextMenuListener()
installTabContextMenuListener()
installTableContextMenuListener()
installTreeContextMenuListener()
app.mount('#app')
