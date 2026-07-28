<script setup>
import { computed, ref } from 'vue'
import { SetLanguage } from '../../wailsjs/go/main/App'
import { language, storeLanguage, translate as t } from '../i18n'

const applying = ref(false)
const current = computed(() => language.value)

async function selectLanguage(next) {
  if (applying.value || next === current.value) return
  applying.value = true
  try {
    await SetLanguage(next)
    storeLanguage(next)
    window.setTimeout(() => window.location.reload(), 80)
  } catch (error) {
    console.error('Failed to change language:', error)
    applying.value = false
  }
}
</script>

<template>
  <section class="language-panel">
    <div class="section-header">
      <div>
        <h2>{{ t('language.title') }}</h2>
        <p>{{ t('language.hint') }}</p>
      </div>
      <span class="current-language">{{ t('language.current') }}: {{ current === 'en' ? t('language.english') : t('language.chinese') }}</span>
    </div>

    <div class="language-grid">
      <article class="language-card" :class="{ active: current === 'en' }">
        <div class="language-icon">EN</div>
        <div class="language-copy">
          <div class="language-title-row">
            <h3>{{ t('language.english') }}</h3>
            <span class="default-badge">{{ t('language.default') }}</span>
          </div>
          <p>{{ t('language.englishDescription') }}</p>
        </div>
        <button class="language-button" :disabled="applying || current === 'en'" @click="selectLanguage('en')">
          {{ current === 'en' ? t('language.active') : applying ? t('language.switching') : t('language.switch') }}
        </button>
      </article>

      <article class="language-card" :class="{ active: current === 'zh' }">
        <div class="language-icon">中</div>
        <div class="language-copy">
          <div class="language-title-row">
            <h3>{{ t('language.chinese') }}</h3>
          </div>
          <p>{{ t('language.chineseDescription') }}</p>
        </div>
        <button class="language-button" :disabled="applying || current === 'zh'" @click="selectLanguage('zh')">
          {{ current === 'zh' ? t('language.active') : applying ? t('language.switching') : t('language.switch') }}
        </button>
      </article>
    </div>
  </section>
</template>

<style scoped>
.language-panel {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.section-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  padding: 18px;
  border: 1px solid rgba(255,255,255,0.08);
  border-radius: 16px;
  background: rgba(255,255,255,0.045);
}
h2 { margin: 0; color: rgba(255,255,255,0.9); font-size: 1.1rem; }
.section-header p { margin: 7px 0 0; color: rgba(255,255,255,0.45); font-size: 0.8rem; line-height: 1.5; }
.current-language {
  flex-shrink: 0;
  padding: 6px 10px;
  border: 1px solid rgba(103,232,249,0.18);
  border-radius: 99px;
  color: rgba(103,232,249,0.85);
  background: rgba(103,232,249,0.06);
  font-size: 0.72rem;
}
.language-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.language-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  border: 1px solid rgba(255,255,255,0.09);
  border-radius: 12px;
  background: linear-gradient(145deg, rgba(255,255,255,0.06), rgba(255,255,255,0.025));
  transition: transform .2s, border-color .2s;
}
.language-card:hover { transform: translateY(-2px); border-color: rgba(103,232,249,0.22); }
.language-card.active { border-color: rgba(103,232,249,0.5); background: linear-gradient(145deg, rgba(103,232,249,0.13), rgba(99,102,241,0.08)); }
.language-icon {
  width: 36px;
  height: 36px;
  display: grid;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid rgba(255,255,255,0.13);
  border-radius: 10px;
  color: rgba(255,255,255,0.8);
  background: rgba(255,255,255,0.04);
  font-size: 0.78rem;
  font-weight: 700;
}
.language-copy { min-width: 0; }
.language-title-row { display: flex; align-items: center; gap: 8px; }
h3 { margin: 0; color: rgba(255,255,255,0.86); font-size: 0.86rem; }
.language-copy p { margin: 7px 0 0; color: rgba(255,255,255,0.42); font-size: 0.76rem; line-height: 1.45; }
.default-badge { padding: 2px 6px; border-radius: 99px; color: rgba(251,191,36,0.95); background: rgba(251,191,36,0.1); font-size: 0.62rem; }
.language-button {
  margin-left: auto;
  padding: 6px 9px;
  border: 1px solid rgba(103,232,249,0.28);
  border-radius: 7px;
  color: rgba(103,232,249,0.92);
  background: rgba(103,232,249,0.07);
  font: inherit;
  font-size: 0.7rem;
  cursor: pointer;
}
.language-button:not(:disabled):hover { background: rgba(103,232,249,0.18); }
.language-button:disabled { cursor: default; opacity: 0.55; }
.language-card.active .language-button { color: #4ade80; border-color: rgba(74,222,128,0.25); background: rgba(74,222,128,0.09); }
@media (max-width: 680px) {
  .language-grid { grid-template-columns: 1fr; }
}
</style>
