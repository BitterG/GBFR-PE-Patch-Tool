import { computed, ref } from 'vue'

const STORAGE_KEY = 'gbfr-pe-patch-tool.language'
const supportedLanguages = new Set(['en', 'zh'])

export const language = ref(normalizeLanguage(localStorage.getItem(STORAGE_KEY) || 'zh'))
export const hasStoredLanguage = () => localStorage.getItem(STORAGE_KEY) !== null

function normalizeLanguage(value) {
  return supportedLanguages.has(value) ? value : 'zh'
}

export function getStoredLanguage() {
  return language.value
}

export function storeLanguage(value) {
  const next = normalizeLanguage(value)
  localStorage.setItem(STORAGE_KEY, next)
  language.value = next
  document.documentElement.lang = next === 'zh' ? 'zh-CN' : 'en'
  return next
}

const localeModules = import.meta.glob('./locales/*/*.json', {
  eager: true,
  import: 'default',
})

const messages = Object.entries(localeModules).reduce((catalog, [path, entries]) => {
  const match = path.match(/\.\/locales\/(en|zh)\//)
  if (!match) return catalog

  const locale = match[1]
  catalog[locale] = { ...catalog[locale], ...entries }
  return catalog
}, { en: {}, zh: {} })

function readMessage(locale, key) {
  return key.split('.').reduce((value, segment) => value?.[segment], messages[locale])
}

function interpolate(message, params) {
  if (!params || typeof message !== 'string') return message
  return message.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? `{${name}}`))
}

// JSON dictionaries use stable semantic keys. Missing translations fall back to
// English, then the key itself, so the UI never silently renders an empty label.
export function translate(key, params) {
  const current = readMessage(language.value, key)
  const fallback = readMessage('en', key)
  return interpolate(current ?? fallback ?? key, params)
}

export function getMissingTranslationKeys(locale) {
  const missing = []
  const visit = (value, prefix = '') => {
    for (const [name, child] of Object.entries(value)) {
      const key = prefix ? `${prefix}.${name}` : name
      if (typeof child === 'string') {
        if (readMessage(locale, key) == null) missing.push(key)
      } else if (child && typeof child === 'object') {
        visit(child, key)
      }
    }
  }
  visit(messages.en)
  return missing
}

export function useI18n() {
  return {
    language,
    t: translate,
    isChinese: computed(() => language.value === 'zh'),
  }
}
