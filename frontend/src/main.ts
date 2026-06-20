import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { installConnectionContextMenuListener } from './api/connectionContextMenu'
import { installGridContextMenuListener } from './api/gridContextMenu'
import { installSidebarContextMenuListener } from './api/sidebarContextMenu'
import { installTabContextMenuListener } from './api/tabContextMenu'
import { installTableContextMenuListener } from './api/tableContextMenu'
import { installTreeContextMenuListener } from './api/treeContextMenu'
import './styles/global.css'

const app = createApp(App)
app.use(createPinia())
// Subscribe once to Wails native context-menu actions.
installConnectionContextMenuListener()
installGridContextMenuListener()
installSidebarContextMenuListener()
installTabContextMenuListener()
installTableContextMenuListener()
installTreeContextMenuListener()
app.mount('#app')
