import { createApp } from 'vue'
import './style.css'
import App from './App.vue'
import { SetLanguage } from '../wailsjs/go/main/App'
import { getStoredLanguage } from './i18n'

async function bootstrap() {
  const selectedLanguage = getStoredLanguage()
  try {
    await SetLanguage(selectedLanguage)
  } catch (error) {
    console.warn('Unable to set backend language before startup:', error)
  }

  createApp(App).mount('#app')
}

bootstrap()
