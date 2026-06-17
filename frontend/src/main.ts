import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { installGridContextMenuListener } from './api/gridContextMenu'
import './styles/global.css'

const app = createApp(App)
app.use(createPinia())
// Subscribe once to Wails native context-menu actions for the data grid.
installGridContextMenuListener()
app.mount('#app')
