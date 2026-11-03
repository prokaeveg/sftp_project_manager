import {createApp} from 'vue'
import App from './App.vue'
import './style.css'
import { initializeBackend } from './bridge'

await initializeBackend()
createApp(App).mount('#app')
