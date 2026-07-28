import { match as matchPinyin } from 'pinyin-pro'
import { language } from '../i18n.js'

export function matchText(input, search) {
  const a = String(input ?? '').toLowerCase()
  const translated = a
  const b = String(search ?? '').trim().toLowerCase()
  if (!b) return true
  if (a.includes(b) || translated.includes(b)) return true
  if (language.value === 'zh' && !!matchPinyin(a, b)) return true
  return false
}
