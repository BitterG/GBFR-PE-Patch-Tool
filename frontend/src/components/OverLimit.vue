<script setup>
import { translate as t } from '../i18n'
import { onMounted, reactive, ref } from 'vue'
import {
  OverLimitCommit,
  OverLimitEnable,
  OverLimitGetOptions,
  OverLimitGetStatus,
  OverLimitScan,
  OverLimitSetSlot,
} from '../../wailsjs/go/main/App'

const emit = defineEmits(['status'])

const loading = ref(false)
const options = reactive({ attributes: [], levels: [] })
const status = reactive({ found: false, hooked: false, address: 0, rva: 0, selectedAddr: 0, commitRva: 0, currentBytes: '', slots: [] })
const edits = reactive([
  { attribute: 0, level: 0, value: 0 },
  { attribute: 0, level: 0, value: 0 },
  { attribute: 0, level: 0, value: 0 },
  { attribute: 0, level: 0, value: 0 },
])

onMounted(() => {
  OverLimitGetOptions()
    .then((res) => {
      options.attributes = res.attributes || []
      options.levels = res.levels || []
    })
    .catch((err) => emit('status', String(err), 'error'))
})

function formatHex(value) {
  if (!value) return '-'
  return '0x' + Number(value).toString(16).toUpperCase()
}

function attributeName(id) {
  const option = attributeOption(id)
  return option ? t(`runtimeTools.overLimit.attributes.${option.hex}`) : formatHex(id)
}

function levelName(id) {
  const option = options.levels.find(x => Number(x.id) === Number(id))
  return option ? t(`runtimeTools.overLimit.levels.${option.hex}`) : formatHex(id)
}

function attributeOption(id) {
  return options.attributes.find(x => Number(x.id) === Number(id))
}

function maxValue(id) {
  const opt = attributeOption(id)
  return opt ? Number(opt.maxValue || 0) : 0
}

function applyMaxValue(index) {
  edits[index].value = maxValue(edits[index].attribute)
}

function applyStatus(next) {
  Object.assign(status, next || { found: false, hooked: false, address: 0, rva: 0, selectedAddr: 0, commitRva: 0, currentBytes: '', slots: [] })
  ;(status.slots || []).forEach((slot, i) => {
    if (i < edits.length) {
      edits[i].attribute = Number(slot.attribute || 0)
      edits[i].level = Number(slot.level || 0)
      edits[i].value = Number(slot.value || maxValue(slot.attribute) || 0)
    }
  })
}

function run(action, success) {
  loading.value = true
  action()
    .then((res) => { applyStatus(res); if (success) emit('status', success, 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function scan() {
  run(() => OverLimitScan(), t('runtimeTools.overLimit.scanSuccess'))
}

function enable() {
  run(() => OverLimitEnable(), t('runtimeTools.overLimit.enableSuccess'))
}

function refresh() {
  loading.value = true
  OverLimitGetStatus()
    .then((res) => {
      applyStatus(res)
      emit('status', res?.selectedAddr ? t('runtimeTools.overLimit.refreshSuccess') : t('runtimeTools.overLimit.refreshMissing'), res?.selectedAddr ? 'success' : 'error')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function writeSaveAll() {
  run(
    () => edits.reduce((p, edit, index) => p.then(() => OverLimitSetSlot({ index, attribute: Number(edit.attribute), level: Number(edit.level), value: Number(edit.value) })), Promise.resolve()).then(() => OverLimitCommit()),
    t('runtimeTools.overLimit.writeSuccess')
  )
}
</script>

<template>
  <div class="root">
    <div class="section">
      <div class="header">
        <span class="title">{{ t('runtimeTools.overLimit.title') }}</span>
        <span class="info-dot" :title="t('runtimeTools.overLimit.notice')">!</span>
        <span class="hint">{{ t('runtimeTools.overLimit.hint') }}</span>
      </div>

      <div class="memory-card guide-card">
        <div class="memory-header">
          <span class="memory-title">{{ t('runtimeTools.overLimit.guideTitle') }}</span>
          <span class="memory-hint">{{ t('runtimeTools.overLimit.guideHint') }}</span>
        </div>
        <ol class="guide-list">
          <li>{{ t('runtimeTools.overLimit.guide1') }}</li>
          <li>{{ t('runtimeTools.overLimit.guide2') }}</li>
          <li>{{ t('runtimeTools.overLimit.guide3') }}</li>
          <li>{{ t('runtimeTools.overLimit.guide4') }}</li>
          <li>{{ t('runtimeTools.overLimit.guide5') }}</li>
        </ol>
      </div>

      <div class="memory-card" :class="{ active: status.hooked }">
        <div class="memory-header">
          <span class="memory-title">{{ t('runtimeTools.overLimit.reader') }}</span>
        </div>
        <div class="memory-info">
          <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(status.rva) }}</span>
          <span>{{ t('runtimeTools.common.status') }}: {{ status.hooked ? t('runtimeTools.common.enabled') : t('runtimeTools.common.disabled') }}</span>
          <span>{{ t('runtimeTools.overLimit.characterData', { address: formatHex(status.selectedAddr) }) }}</span>
        </div>
        <div class="memory-row">
          <button class="btn-batch" @click="enable" :disabled="loading || status.hooked">{{ t('runtimeTools.overLimit.enableRead') }}</button>
          <button class="btn-refresh" @click="refresh" :disabled="loading">{{ t('runtimeTools.common.refresh') }}</button>
          <button class="btn-sort" @click="scan" :disabled="loading">{{ t('runtimeTools.common.rescan') }}</button>
          <button class="btn-batch" @click="writeSaveAll" :disabled="loading || !status.selectedAddr">{{ t('runtimeTools.overLimit.writeResults') }}</button>
        </div>
        <div class="memory-bytes">{{ status.currentBytes || t('runtimeTools.common.notLocated') }}</div>
      </div>

      <div v-if="!status.selectedAddr" class="empty">{{ t('runtimeTools.overLimit.empty') }}</div>

      <div v-else class="slot-list">
        <div v-for="slot in status.slots" :key="slot.index" class="memory-card slot-card">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.overLimit.ability', { index: slot.index + 1 }) }}</span>
            <span class="memory-hint">{{ t('runtimeTools.overLimit.current', { attribute: attributeName(slot.attribute), level: levelName(slot.level) }) }}</span>
          </div>
          <div class="slot-grid">
            <label>
              <span>{{ t('runtimeTools.overLimit.attribute') }}</span>
              <select v-model.number="edits[slot.index].attribute" class="od-select" @change="applyMaxValue(slot.index)">
                <option v-for="opt in options.attributes" :key="opt.id" :value="opt.id">{{ attributeName(opt.id) }} ({{ opt.hex }})</option>
              </select>
            </label>
            <label>
              <span>{{ t('runtimeTools.overLimit.level') }}</span>
              <select v-model.number="edits[slot.index].level" class="od-select level-select">
                <option v-for="opt in options.levels" :key="opt.id" :value="opt.id">{{ levelName(opt.id) }}</option>
              </select>
            </label>
            <label class="value-edit">
              <span>{{ t('runtimeTools.overLimit.value') }}</span>
              <input v-model.number="edits[slot.index].value" type="number" min="0" :max="maxValue(edits[slot.index].attribute)" step="1" class="value-input" />
            </label>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.root { display:flex; flex-direction:column; gap:10px; width:100%; max-width:720px; margin:0 auto; padding-bottom:40px; }
.section { border-radius:16px; padding:16px 18px; background:linear-gradient(135deg, rgba(56,189,248,0.12) 0%, rgba(103,232,249,0.06) 100%); border:1px solid rgba(103,232,249,0.15); display:flex; flex-direction:column; gap:10px; }
.header { display:flex; align-items:center; justify-content:space-between; gap:8px; }
.title { font-size:0.88rem; font-weight:600; color:rgba(255,255,255,0.65); letter-spacing:1px; }
.info-dot { display:inline-flex; align-items:center; justify-content:center; width:15px; height:15px; border-radius:50%; border:1px solid rgba(103,232,249,0.35); color:#67e8f9; background:rgba(103,232,249,0.08); font-size:0.68rem; font-weight:700; cursor:help; flex-shrink:0; }
.hint { font-size:0.68rem; color:rgba(255,255,255,0.25); margin-left:auto; }
.memory-card { position:relative; overflow:hidden; z-index:0; border-radius:12px; padding:12px; background:rgba(255,255,255,0.045); border:1px solid rgba(165,180,252,0.16); box-shadow:0 10px 26px rgba(0,0,0,0.18); display:flex; flex-direction:column; gap:8px; transition:border-color 0.3s, box-shadow 0.3s; }
.memory-card::after { content:""; position:absolute; inset:0; z-index:-1; border-radius:12px; background:#abd373; transform:translateY(calc(-100% - 2px)); transition:transform 0.5s ease; }
.memory-card.active { border-color:rgba(171,211,115,0.55); box-shadow:0 14px 34px rgba(171,211,115,0.18); }
.memory-card.active::after { transform:translateY(0); }
.memory-card.active .memory-title { color:#1f2937; }
.memory-card.active .memory-hint, .memory-card.active .memory-info, .memory-card.active .memory-bytes { color:rgba(31,41,55,0.72); }
.memory-card.active .btn-batch { border-color:rgba(31,41,55,0.22); background:rgba(31,41,55,0.12); color:#1f2937; }
.memory-card.active .btn-refresh, .memory-card.active .btn-sort { border-color:rgba(31,41,55,0.16); background:rgba(255,255,255,0.18); color:rgba(31,41,55,0.72); }
.memory-header, .memory-info, .memory-row { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
.memory-header .memory-hint { margin-left:auto; }
.memory-title { font-size:0.8rem; font-weight:600; color:rgba(255,255,255,0.62); }
.memory-hint, .memory-info { font-size:0.68rem; color:rgba(255,255,255,0.32); }
.memory-bytes { font-size:0.66rem; color:rgba(255,255,255,0.24); font-family:'Courier New',monospace; word-break:break-all; }
.guide-card { gap:10px; }
.guide-list { margin:0; padding-left:18px; color:rgba(255,255,255,0.46); font-size:0.72rem; line-height:1.65; }
.guide-list li { padding-left:2px; }
.slot-list { display:flex; flex-direction:column; gap:10px; }
.slot-card::after { display:none; }
.slot-grid { display:grid; grid-template-columns:minmax(210px, 1fr) 96px 78px; gap:8px; align-items:end; }
.slot-grid label, .slot-value { display:flex; flex-direction:column; gap:5px; text-align:left; }
.slot-grid label span, .slot-value span { font-size:0.68rem; color:rgba(255,255,255,0.32); }
.slot-value strong, .value-input { min-height:30px; display:flex; align-items:center; color:#67e8f9; font-size:0.82rem; }
.value-input { box-sizing:border-box; width:100%; padding:6px 8px; border-radius:6px; border:1px solid rgba(255,255,255,0.15); background:rgba(255,255,255,0.07); outline:none; }
.od-select { width:100%; padding:6px 10px; border-radius:6px; border:1px solid rgba(255,255,255,0.15); background:rgba(255,255,255,0.07); color:#fff; font-size:0.8rem; outline:none; cursor:pointer; }
.od-select:focus { border-color:rgba(103,232,249,0.5); }
.od-select option { background:#1a1a2e; color:#fff; }
.btn-batch { padding:6px 14px; border-radius:6px; border:1px solid rgba(165,180,252,0.3); background:rgba(165,180,252,0.1); color:#a5b4fc; font-size:0.78rem; font-weight:600; cursor:pointer; transition:background 0.2s; white-space:nowrap; }
.btn-batch:not(:disabled):hover { background:rgba(165,180,252,0.2); }
.btn-batch:disabled { opacity:0.4; cursor:not-allowed; }
.btn-refresh, .btn-sort { padding:6px 14px; border-radius:6px; border:1px solid rgba(255,255,255,0.12); background:rgba(255,255,255,0.05); color:rgba(255,255,255,0.5); font-size:0.78rem; font-weight:600; cursor:pointer; transition:background 0.2s; }
.btn-refresh:hover, .btn-sort:hover { background:rgba(255,255,255,0.1); color:rgba(255,255,255,0.7); }
.btn-refresh:disabled, .btn-sort:disabled { opacity:0.4; cursor:not-allowed; }
.empty { font-size:0.78rem; color:rgba(255,255,255,0.3); text-align:center; padding:12px 0; }
@media (max-width: 640px) { .slot-grid { grid-template-columns:1fr 1fr; } .slot-grid .btn-batch { grid-column:1 / -1; } }
</style>
