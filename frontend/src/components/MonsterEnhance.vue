<script setup>
import { translate as t } from '../i18n'
import { reactive, ref } from 'vue'
import { MonsterEnhanceGetStatus, MonsterEnhanceSetPatchValueEnabled, DamageMeterGetStatus } from '../../wailsjs/go/main/App'

const emit = defineEmits(['status'])

const defaultMultipliers = { monster_hp: '1', monster_stun: '1', monster_damage_new: '1', sba_chain_timer: '3', od_rate: '1' }
const sessionMultipliers = window.gbfrMonsterEnhanceMultipliers || (window.gbfrMonsterEnhanceMultipliers = { ...defaultMultipliers })

const loading = ref(false)
const result = reactive({ pid: 0, dllPath: '', injected: false, enabled: false, currentBytes: '', items: [] })
const multipliers = reactive(sessionMultipliers)
const overdriveState = ref('3')

const hiddenUnfixedItems = new Set([
  'link_time_no_drain',
  'link_time_disable',
  'monster_damage',
  'sba_chain_timer',
  'purple_drain',
  'blue_grow',
  'blue_drain',
])

function isVisibleItem(item) {
  return item.id !== 'inventory_set_45' && !hiddenUnfixedItems.has(item.id)
}

function applyResult(res) {
  const previous = new Map((result.items || []).map(item => [item.id, item]))
  const incoming = (res && res.items) || []
  result.pid = (res && res.pid) || 0
  result.dllPath = (res && res.dllPath) || result.dllPath || ''
  result.injected = !!(res && res.injected)
  result.enabled = !!(res && res.enabled)
  result.currentBytes = (res && res.currentBytes) || ''
  result.items = incoming.filter(isVisibleItem).map((item) => Object.assign(previous.get(item.id) || {}, item))
}

function refreshStatus() {
  loading.value = true
  MonsterEnhanceGetStatus()
    .then((res) => applyResult(res))
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function needsMultiplier(item) {
  return item.id === 'monster_hp' || item.id === 'monster_stun' || item.id === 'monster_damage_new' || item.id === 'sba_chain_timer' || item.id === 'od_rate'
}

function needsOverdriveState(item) {
  return item.id === 'overdrive_state'
}

function needsSbaTimer(item) {
  return item.id === 'sba_chain_timer'
}

function itemName(item) {
  return t(`runtimeTools.monsterEnhance.items.${item.id}`)
}

function multiplierHint(item) {
  if (item.id === 'monster_hp') return t('runtimeTools.monsterEnhance.hintHp')
  if (item.id === 'monster_stun') return t('runtimeTools.monsterEnhance.hintStun')
  if (item.id === 'monster_damage_new') return t('runtimeTools.monsterEnhance.hintDamageNew')
  if (item.id === 'sba_chain_timer') return t('runtimeTools.monsterEnhance.hintSba')
  if (item.id === 'od_rate') return t('runtimeTools.monsterEnhance.hintOdRate')
  return ''
}

function getMultiplier(item) {
  return parseFloat(multipliers[item.id] || defaultMultipliers[item.id] || '1')
}

function patchValue(item) {
  return needsOverdriveState(item) ? parseInt(overdriveState.value, 10) : (needsMultiplier(item) ? getMultiplier(item) : 0)
}

function startsDamageMeter(item) {
  return item.id === 'monster_hp'
}

function ensureDamageMeter() {
  return DamageMeterGetStatus().catch((err) => emit('status', t('runtimeTools.monsterEnhance.damageMeterFailed', { error: String(err) }), 'error'))
}

function setOne(item, enabled, id = item.id) {
  if (enabled && needsMultiplier(item)) {
    const v = getMultiplier(item)
    if (isNaN(v) || v <= 0 || v > 9999) { emit('status', t('runtimeTools.monsterEnhance.multiplierInvalid'), 'error'); return }
  }
  if (enabled && needsOverdriveState(item)) {
    const v = patchValue(item)
    if (![0, 3, 9].includes(v)) { emit('status', t('runtimeTools.monsterEnhance.overdriveInvalid'), 'error'); return }
  }
  const previous = item.enabled
  item.enabled = enabled
  loading.value = true
  MonsterEnhanceSetPatchValueEnabled(id, enabled, patchValue(item))
    .then((res) => {
      if (enabled && startsDamageMeter(item)) ensureDamageMeter()
      applyResult(res)
      const verb = id === 'overdrive_state_apply' || (item.id === 'sba_chain_timer' && enabled) ? t('runtimeTools.monsterEnhance.applied') : (enabled ? t('runtimeTools.monsterEnhance.started') : t('runtimeTools.monsterEnhance.stopped'))
      emit('status', t('runtimeTools.monsterEnhance.status', { name: itemName(item), verb }), 'success')
    })
    .catch((err) => {
      item.enabled = previous
      emit('status', String(err), 'error')
    })
    .finally(() => { loading.value = false })
}

refreshStatus()
</script>

<template>
  <div class="root">
    <div class="section">
      <div class="header">
        <span class="title">{{ t('runtimeTools.monsterEnhance.title') }}</span>
        <span class="info-dot" :title="t('runtimeTools.monsterEnhance.notice')">!</span>
        <span class="hint">{{ t('runtimeTools.monsterEnhance.hint') }}</span>
      </div>

      <div class="process-card">
        <div class="memory-info">
          <span>{{ t('runtimeTools.monsterEnhance.process') }}</span>
          <span v-if="result.pid">{{ t('runtimeTools.common.pid') }}: {{ result.pid }}</span>
          <button class="btn-refresh compact" @click="refreshStatus" :disabled="loading">{{ t('runtimeTools.common.refresh') }}</button>
        </div>
      </div>

      <div class="card-grid">
        <div v-for="item in result.items" :key="item.id" class="memory-card" :class="{ active: item.enabled }">
        <div class="memory-header">
          <span class="memory-title">{{ itemName(item) }}</span>
          <span class="state" :class="{ on: item.enabled }">{{ item.enabled ? t('runtimeTools.common.enabled') : t('runtimeTools.common.disabled') }}</span>
          <span class="memory-hint">{{ t('runtimeTools.common.rva') }}: 0x{{ Number(item.rva).toString(16).toUpperCase() }}</span>
        </div>
        <div v-if="needsMultiplier(item)" class="memory-row">
          <input v-model="multipliers[item.id]" type="number" min="0.1" max="9999" step="0.1" class="batch-input" :placeholder="t('runtimeTools.monsterEnhance.multiplier')" />
          <span class="memory-hint">{{ multiplierHint(item) }}</span>
        </div>
        <div v-if="needsOverdriveState(item)" class="memory-row">
          <select v-model="overdriveState" class="batch-input od-select">
            <option value="0">{{ t('runtimeTools.monsterEnhance.overdrive.empty') }}</option>
            <option value="3">{{ t('runtimeTools.monsterEnhance.overdrive.full') }}</option>
            <option value="9">{{ t('runtimeTools.monsterEnhance.overdrive.auto') }}</option>
          </select>
          <span class="memory-hint">{{ t('runtimeTools.monsterEnhance.overdrive.hint') }}</span>
        </div>
        <div class="memory-row" v-if="needsOverdriveState(item)">
          <button class="btn-batch" @click="setOne(item, true, 'overdrive_state_apply')" :disabled="loading || overdriveState === '9'">{{ t('runtimeTools.monsterEnhance.overdrive.applyOnce') }}</button>
          <button class="btn-batch" @click="setOne(item, true)" :disabled="loading || item.enabled || overdriveState !== '9'">{{ t('runtimeTools.monsterEnhance.overdrive.auto') }}</button>
          <button class="btn-refresh" @click="setOne(item, false)" :disabled="loading || !item.enabled">{{ t('runtimeTools.common.disable') }}</button>
        </div>
        <div class="memory-row" v-else-if="needsSbaTimer(item)">
          <button class="btn-batch" @click="setOne(item, true)" :disabled="loading">{{ t('runtimeTools.common.apply') }}</button>
          <button class="btn-refresh" @click="setOne(item, false)" :disabled="loading || !item.enabled">{{ t('runtimeTools.common.restoreDefault') }}</button>
        </div>
        <div class="memory-row" v-else>
          <button class="btn-batch" @click="setOne(item, true)" :disabled="loading || item.enabled">{{ t('runtimeTools.common.enable') }}</button>
          <button class="btn-refresh" @click="setOne(item, false)" :disabled="loading || !item.enabled">{{ t('runtimeTools.common.disable') }}</button>
        </div>
        <div class="memory-bytes">{{ item.currentBytes }}</div>
        </div>
        <div class="memory-card custom-note-card">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.monsterEnhance.noteTitle') }}</span>
          </div>
          <div class="custom-note-text">
            <strong class="note-warn">{{ t('runtimeTools.monsterEnhance.noteWarn') }}</strong>，
            {{ t('runtimeTools.monsterEnhance.note') }}
          </div>
        </div>
      </div>

      <div v-if="!result.items.length" class="empty">{{ t('runtimeTools.monsterEnhance.empty') }}</div>
    </div>
  </div>
</template>

<style scoped>
.root { display:flex; flex-direction:column; gap:10px; width:100%; max-width:720px; margin:0 auto; padding-bottom:40px; }
.section {
  border-radius:16px; padding:16px 18px;
  background:linear-gradient(135deg, rgba(56,189,248,0.12) 0%, rgba(103,232,249,0.06) 100%);
  border:1px solid rgba(103,232,249,0.15);
  display:flex; flex-direction:column; gap:10px;
}
.header { display:flex; align-items:center; justify-content:space-between; gap:8px; }
.title { font-size:0.88rem; font-weight:600; color:rgba(255,255,255,0.65); letter-spacing:1px; }
.info-dot { display:inline-flex; align-items:center; justify-content:center; width:15px; height:15px; border-radius:50%; border:1px solid rgba(103,232,249,0.35); color:#67e8f9; background:rgba(103,232,249,0.08); font-size:0.68rem; font-weight:700; cursor:help; flex-shrink:0; }
.hint { font-size:0.68rem; color:rgba(255,255,255,0.25); margin-left:auto; }
.process-card {
  border-radius:10px; padding:8px 10px;
  background:rgba(255,255,255,0.035); border:1px solid rgba(103,232,249,0.12);
}
.card-grid { display:grid; grid-template-columns:repeat(2, minmax(0, 1fr)); gap:10px; }
.memory-card {
  position:relative; overflow:hidden; z-index:0;
  border-radius:12px; padding:12px;
  background:rgba(255,255,255,0.045); border:1px solid rgba(165,180,252,0.16);
  box-shadow:0 10px 26px rgba(0,0,0,0.18);
  display:flex; flex-direction:column; gap:8px;
  transition:border-color 0.3s, box-shadow 0.3s, transform 0.3s;
}
.memory-card::after {
  content:""; position:absolute; inset:0; z-index:-1; border-radius:12px;
  background:#abd373; transform:translateY(calc(-100% - 2px));
  transition:transform 0.5s ease;
}
.memory-card.active { border-color:rgba(171,211,115,0.55); box-shadow:0 14px 34px rgba(171,211,115,0.18); }
.memory-card.active::after { transform:translateY(0); }
.memory-card.active .memory-title { color:#1f2937; }
.memory-card.active .memory-hint,
.memory-card.active .memory-info,
.memory-card.active .memory-bytes,
.memory-card.active .custom-note-text { color:rgba(31,41,55,0.72); }
.memory-card.active .state { color:#1f2937; background:rgba(255,255,255,0.2); border-color:rgba(31,41,55,0.18); }
.memory-card.active .btn-batch { border-color:rgba(31,41,55,0.22); background:rgba(31,41,55,0.12); color:#1f2937; }
.memory-card.active .btn-refresh { border-color:rgba(31,41,55,0.16); background:rgba(255,255,255,0.18); color:rgba(31,41,55,0.72); }
.memory-card.active .batch-input { border-color:rgba(31,41,55,0.22); background:rgba(255,255,255,0.22); color:#1f2937; color-scheme:light; }
.custom-note-card { min-height:96px; }
.custom-note-text { font-size:0.76rem; line-height:1.55; color:rgba(255,255,255,0.46); }
.memory-header, .memory-info, .memory-row { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
.memory-header { justify-content:flex-start; }
.memory-header .memory-hint { margin-left:auto; }
.memory-title { font-size:0.8rem; font-weight:600; color:rgba(255,255,255,0.62); }
.memory-hint, .memory-info { font-size:0.68rem; color:rgba(255,255,255,0.32); }
.batch-input { width:80px; padding:6px 10px; border-radius:6px; border:1px solid rgba(255,255,255,0.15); background:rgba(255,255,255,0.07); color:#fff; font-size:0.82rem; outline:none; color-scheme:dark; }
.batch-input::-webkit-inner-spin-button, .batch-input::-webkit-outer-spin-button { filter:invert(1) opacity(0.7); }
.od-select { width:120px; }
.od-select option { background:#111827; color:#e5e7eb; }
.memory-bytes { font-size:0.66rem; color:rgba(255,255,255,0.24); font-family:'Courier New',monospace; word-break:break-all; }
.btn-batch, .btn-refresh {
  padding:6px 14px; border-radius:6px; font-size:0.78rem; font-weight:600; cursor:pointer;
  transition:background 0.2s; white-space:nowrap;
}
.btn-batch { border:1px solid rgba(165,180,252,0.3); background:rgba(165,180,252,0.1); color:#a5b4fc; }
.btn-refresh { border:1px solid rgba(255,255,255,0.12); background:rgba(255,255,255,0.05); color:rgba(255,255,255,0.5); }
.btn-batch:not(:disabled):hover { background:rgba(165,180,252,0.2); }
.btn-refresh:not(:disabled):hover { background:rgba(255,255,255,0.1); color:rgba(255,255,255,0.7); }
.btn-batch:disabled, .btn-refresh:disabled { opacity:0.4; cursor:not-allowed; }
.btn-refresh.compact { padding:4px 10px; font-size:0.72rem; }
.state { font-size:0.68rem; padding:2px 8px; border-radius:999px; color:#f87171; background:rgba(239,68,68,0.12); border:1px solid rgba(239,68,68,0.22); }
.state.on { color:#4ade80; background:rgba(34,197,94,0.12); border-color:rgba(34,197,94,0.22); }
.empty { font-size:0.78rem; color:rgba(255,255,255,0.3); text-align:center; padding:12px 0; }
@media (max-width: 640px) { .card-grid { grid-template-columns:1fr; } }
.note-warn { color:#67e8f9; }
</style>
