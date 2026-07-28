<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { SigilMemoryGetOptions, SigilMemoryGetStatus, SigilMemoryEnable, SigilMemoryUpdate } from '../../wailsjs/go/main/App'
import { translate as t } from '../i18n'
import { matchText } from '../utils/matchText.js'
import { clearHistory, deleteTemplate, history, pushHistory, renameTemplate, saveTemplate, templates } from '../utils/sigilMemoryStore.js'
import SigilMemoryPicker from './SigilMemoryPicker.vue'

const emit = defineEmits(['status'])

const status = reactive({
  found: false, hooked: false, selectedAddr: 0,
  sigilHash: 0, sigilLevel: 0, sigilName: '',
  primaryTraitHash: 0, primaryTraitLevel: 0, primaryTraitName: '',
  secondaryTraitHash: 0, secondaryTraitLevel: 0, secondaryTraitName: '',
})

const form = reactive({
  sigilHash: 0, sigilLevel: 0,
  primaryTraitHash: 0, primaryTraitLevel: 0,
  secondaryTraitHash: 0, secondaryTraitLevel: 0,
})

const backendOptions = reactive({ sigils: [], traits: [] })
const runtimeOptions = reactive({ sigils: new Map(), traits: new Map() })

const allSigilOptions = computed(() => [...backendOptions.sigils, ...runtimeOptions.sigils.values()])
const allTraitOptions = computed(() => [...backendOptions.traits, ...runtimeOptions.traits.values()])
const secondaryTraitOptions = computed(() => [
  { hash: 0x887AE0B0, displayName: t('sigil.memory.emptySecondary', { hash: '0x887AE0B0' }), source: 'empty' },
  ...allTraitOptions.value,
])

const sigilByHash = computed(() => new Map(allSigilOptions.value.map(o => [o.hash >>> 0, o])))
const traitByHash = computed(() => new Map(allTraitOptions.value.map(o => [o.hash >>> 0, o])))

const loading = ref(false)
const applying = ref(false)
const templateSearch = ref('')
const tab = ref('templates')
const renamingId = ref(null)
const renameBuffer = ref('')

function show(msg, type) { emit('status', msg, type) }
function hex(v) { return '0x' + (Number(v) >>> 0).toString(16).toUpperCase().padStart(8, '0') }
const HEX_RE = /^0x[0-9A-F]{8}$/i
function isRawHexName(n) { return typeof n === 'string' && HEX_RE.test(n.trim()) }

function ensureRuntimeOption(bucket, hash, name) {
  if (!hash) return
  const h = hash >>> 0
  if (bucket.has(h)) return
  const backendList = bucket === runtimeOptions.sigils ? backendOptions.sigils : backendOptions.traits
  if (backendList.some(o => (o.hash >>> 0) === h)) return
  bucket.set(h, {
    hash: h,
    displayName: name && !isRawHexName(name) ? name : t('sigil.memory.unknown', { hash: hex(h) }),
    source: 'runtime',
  })
}

function applyStatus(next) {
  Object.assign(status, next)
  ensureRuntimeOption(runtimeOptions.sigils, next.sigilHash, next.sigilName)
  ensureRuntimeOption(runtimeOptions.traits, next.primaryTraitHash, next.primaryTraitName)
  ensureRuntimeOption(runtimeOptions.traits, next.secondaryTraitHash, next.secondaryTraitName)
}

function syncFormFromStatus() {
  form.sigilHash = status.sigilHash >>> 0
  form.sigilLevel = status.sigilLevel >>> 0
  form.primaryTraitHash = status.primaryTraitHash >>> 0
  form.primaryTraitLevel = status.primaryTraitLevel >>> 0
  form.secondaryTraitHash = status.secondaryTraitHash >>> 0
  form.secondaryTraitLevel = status.secondaryTraitLevel >>> 0
}

async function loadOptions() {
  try {
    const res = await SigilMemoryGetOptions()
    backendOptions.sigils = res.sigils || []
    backendOptions.traits = res.traits || []
  } catch (e) { show(t('sigil.memory.optionsFailed', { error: String(e) }), 'error') }
}

async function refresh(syncForm = false) {
  loading.value = true
  try {
    applyStatus(await SigilMemoryGetStatus())
    if (syncForm) syncFormFromStatus()
    if (!status.hooked) show(t('sigil.memory.ready'), 'success')
    else if (!status.selectedAddr) show(t('sigil.memory.waiting'), 'success')
    else show(t('sigil.memory.read', { name: status.sigilName }), 'success')
  } catch (e) { show(String(e), 'error') }
  finally { loading.value = false }
}

async function enable() {
  loading.value = true
  try {
    applyStatus(await SigilMemoryEnable())
    syncFormFromStatus()
    show(t('sigil.memory.enabled'), 'success')
  } catch (e) { show(String(e), 'error') }
  finally { loading.value = false }
}

async function performWrite() {
  if (!status.hooked || !status.selectedAddr) { show(t('sigil.memory.mustSelect'), 'error'); return }
  applying.value = true
  try {
    const snapshot = { ...form }
    applyStatus(await SigilMemoryUpdate({ ...form }))
    pushHistory(snapshot)
    show(t('sigil.memory.written', { name: status.sigilName }), 'success')
  } catch (e) { show(String(e), 'error') }
  finally { applying.value = false }
}
async function write() { await performWrite() }

async function oneClickMax() {
  if (!status.hooked || !status.selectedAddr) { show(t('sigil.memory.mustSelect'), 'error'); return }
  if (sigilMax.value != null) form.sigilLevel = sigilMax.value
  if (primaryMax.value != null) form.primaryTraitLevel = primaryMax.value
  if (secondaryMax.value != null && form.secondaryTraitHash) form.secondaryTraitLevel = secondaryMax.value
  await performWrite()
}

function onPickSigil(opt) {
  if (opt && opt.maxLevel != null) form.sigilLevel = opt.maxLevel
  else if (!opt) form.sigilLevel = 0
}
function onPickPrimary(opt) {
  if (opt && opt.maxLevel != null) form.primaryTraitLevel = Math.min(15, opt.maxLevel)
  else if (!opt) form.primaryTraitLevel = 0
}
function onPickSecondary(opt) {
  if (opt && opt.maxLevel != null) form.secondaryTraitLevel = Math.min(15, opt.maxLevel)
  else if (!opt) form.secondaryTraitLevel = 0
}

const sigilMax = computed(() => sigilByHash.value.get(form.sigilHash)?.maxLevel ?? null)
// Primary/secondary max come from the picked trait itself, not the sigil's default trait cap.
// Prior bug: primaryMax fell back to sigil.firstTraitMaxLevel even when the picked trait had its own maxLevel,
// so switching to a memory-only trait still showed the sigil's default primary max.
const primaryMax = computed(() => traitByHash.value.get(form.primaryTraitHash)?.maxLevel ?? null)
const secondaryMax = computed(() => traitByHash.value.get(form.secondaryTraitHash)?.maxLevel ?? null)

const sigilAtMax = computed(() => sigilMax.value != null && form.sigilLevel === sigilMax.value)
const primaryAtMax = computed(() => primaryMax.value != null && form.primaryTraitLevel === primaryMax.value)
const secondaryAtMax = computed(() => secondaryMax.value != null && form.secondaryTraitLevel === secondaryMax.value)

function maxSigil() { if (sigilMax.value != null) form.sigilLevel = sigilMax.value }
function maxPrimary() { if (primaryMax.value != null) form.primaryTraitLevel = primaryMax.value }
function maxSecondary() { if (secondaryMax.value != null) form.secondaryTraitLevel = secondaryMax.value }

const canOneClickMax = computed(() => !!status.selectedAddr && (sigilMax.value != null || primaryMax.value != null || (secondaryMax.value != null && !!form.secondaryTraitHash)))

const warnings = computed(() => {
  const out = []
  const sigil = sigilByHash.value.get(form.sigilHash)
  if (sigilMax.value != null && form.sigilLevel > sigilMax.value) out.push(t('sigil.memory.overSigil', { max: sigilMax.value }))
  if (primaryMax.value != null && form.primaryTraitLevel > primaryMax.value) out.push(t('sigil.memory.overPrimary', { max: primaryMax.value }))
  if (secondaryMax.value != null && form.secondaryTraitLevel > secondaryMax.value) out.push(t('sigil.memory.overSecondary', { max: secondaryMax.value }))
  if (form.secondaryTraitHash && sigil && sigil.supportsSecondaryTrait === false) {
    out.push(t('sigil.memory.noSecondary'))
  } else if (
    form.secondaryTraitHash && sigil &&
    Array.isArray(sigil.allowedSecondaryTraitHashes) && sigil.allowedSecondaryTraitHashes.length > 0 &&
    !sigil.allowedSecondaryTraitHashes.map(h => h >>> 0).includes(form.secondaryTraitHash >>> 0)
  ) {
    out.push(t('sigil.memory.secondaryNotAllowed'))
  }
  return out
})

const changedCount = computed(() => {
  let n = 0
  if ((form.sigilHash >>> 0) !== (status.sigilHash >>> 0)) n++
  if ((form.sigilLevel >>> 0) !== (status.sigilLevel >>> 0)) n++
  if ((form.primaryTraitHash >>> 0) !== (status.primaryTraitHash >>> 0)) n++
  if ((form.primaryTraitLevel >>> 0) !== (status.primaryTraitLevel >>> 0)) n++
  if ((form.secondaryTraitHash >>> 0) !== (status.secondaryTraitHash >>> 0)) n++
  if ((form.secondaryTraitLevel >>> 0) !== (status.secondaryTraitLevel >>> 0)) n++
  return n
})

function revertToRead() { syncFormFromStatus() }

function applyEntry(entry) {
  form.sigilHash = entry.sigilHash >>> 0
  form.sigilLevel = entry.sigilLevel >>> 0
  form.primaryTraitHash = entry.primaryTraitHash >>> 0
  form.primaryTraitLevel = entry.primaryTraitLevel >>> 0
  form.secondaryTraitHash = entry.secondaryTraitHash >>> 0
  form.secondaryTraitLevel = entry.secondaryTraitLevel >>> 0
}
async function applyAndWrite(entry) {
  applyEntry(entry)
  await performWrite()
}
function nameFor(map, hash, fallback = '?') {
  const opt = map.get(hash >>> 0)
  if (opt && !isRawHexName(opt.displayName)) return opt.displayName
  return hash ? t('sigil.memory.unknown', { hash: hex(hash) }) : fallback
}

function autoTemplateName() {
  return nameFor(sigilByHash.value, form.sigilHash, t('sigil.memory.emptyTemplate'))
}
function saveCurrentAsTemplate() {
  const saved = saveTemplate(autoTemplateName(), form)
  if (saved) show(t('sigil.memory.templateSaved', { name: saved.name }), 'success')
}

function readDisplay(name, hash) {
  if (!hash) return { text: t('sigil.memory.notSet'), dim: true }
  if (isRawHexName(name)) return { text: t('sigil.memory.unknownEntry'), dim: true }
  return { text: name, dim: false }
}
const sigilRead = computed(() => readDisplay(status.sigilName, status.sigilHash))
const primaryRead = computed(() => readDisplay(status.primaryTraitName, status.primaryTraitHash))
const secondaryRead = computed(() => readDisplay(status.secondaryTraitName, status.secondaryTraitHash))

function entrySubtitle(entry) {
  const s = nameFor(sigilByHash.value, entry.sigilHash, t('sigil.common.none'))
  const p = nameFor(traitByHash.value, entry.primaryTraitHash, t('sigil.common.none'))
  const sec = entry.secondaryTraitHash ? nameFor(traitByHash.value, entry.secondaryTraitHash) : t('sigil.common.none')
  return t('sigil.memory.entrySubtitle', { sigil: s, primary: p, secondary: sec })
}

const filteredTemplates = computed(() => {
  const q = templateSearch.value.trim()
  if (!q) return templates.value
  return templates.value.filter(t => matchText(t.name, q) || matchText(entrySubtitle(t), q))
})

const renameEl = ref(null)

function startRename(id, currentName) {
  renamingId.value = id
  renameBuffer.value = currentName
}
function confirmRename() {
  if (renamingId.value) renameTemplate(renamingId.value, renameBuffer.value)
  renamingId.value = null
  renameBuffer.value = ''
}
function cancelRename() {
  renamingId.value = null
  renameBuffer.value = ''
}

function clearWriteHistory() {
  if (!history.value.length || !window.confirm(t('sigil.memory.recentClearConfirm'))) return
  clearHistory()
  show(t('sigil.memory.recentCleared'), 'success')
}

function onRenameOutsideClick(e) {
  if (!renamingId.value) return
  if (renameEl.value && !renameEl.value.contains(e.target)) cancelRename()
}
watch(renamingId, (v) => {
  if (v) document.addEventListener('mousedown', onRenameOutsideClick)
  else document.removeEventListener('mousedown', onRenameOutsideClick)
})
onBeforeUnmount(() => document.removeEventListener('mousedown', onRenameOutsideClick))
function fmtRelTime(ts) {
  const diffSec = Math.floor((Date.now() - ts) / 1000)
  if (diffSec < 60) return t('sigil.memory.justNow')
  if (diffSec < 3600) return t('sigil.memory.minutesAgo', { count: Math.floor(diffSec / 60) })
  if (diffSec < 86400) return t('sigil.memory.hoursAgo', { count: Math.floor(diffSec / 3600) })
  return t('sigil.memory.daysAgo', { count: Math.floor(diffSec / 86400) })
}

const statusLabel = computed(() => {
  if (status.hooked) return t('sigil.memory.enabledLabel')
  if (status.found) return t('sigil.memory.readyLabel')
  return t('sigil.memory.disconnected')
})

onMounted(async () => {
  await loadOptions()
  await refresh(true)
})
</script>

<template>
  <div class="memory-sigil">
    <!-- Connection strip -->
    <div class="section conn-section">
      <div class="conn-row">
        <div class="conn-left">
          <span class="chip" :class="{ state: status.hooked, dim: !status.hooked }">● {{ statusLabel }}</span>
          <span v-if="!status.hooked && status.found" class="hint-inline">{{ t('sigil.memory.enableHint') }}</span>
          <span v-else-if="status.hooked && !status.selectedAddr" class="hint-inline">{{ t('sigil.memory.waitHint') }}</span>
        </div>
        <div class="conn-right">
          <button v-if="status.hooked" class="btn tiny" :disabled="loading" @click="refresh(true)">{{ loading ? t('sigil.memory.refreshing') : t('sigil.memory.refresh') }}</button>
          <button class="btn tiny btn-cyan" :disabled="loading" @click="enable">{{ status.hooked ? t('sigil.memory.reconnect') : t('sigil.memory.enable') }}</button>
        </div>
      </div>
    </div>

    <!-- Editor -->
    <div class="section" :class="{ muted: !status.selectedAddr }">
      <div class="editor-header">
        <span class="section-title">{{ t('sigil.memory.editor') }}</span>
        <div class="editor-actions">
          <button class="ed-link" :disabled="!status.selectedAddr" @click="saveCurrentAsTemplate" :title="t('sigil.memory.saveTemplateTitle')">{{ t('sigil.memory.saveTemplate') }}</button>
          <button class="ed-link" :disabled="!status.selectedAddr || changedCount === 0" @click="revertToRead" :title="t('sigil.memory.revertTitle')">{{ t('sigil.memory.revert') }}</button>
        </div>
      </div>

      <div class="ed-row">
        <span class="ed-label">{{ t('sigil.generator.sigil') }}</span>
        <div class="ed-current">
          <span class="ed-current-name" :class="{ dim: sigilRead.dim }">{{ sigilRead.text }}</span>
          <span v-if="status.sigilHash" class="ed-current-lv">{{ t('sigil.common.level', { level: status.sigilLevel }) }}</span>
        </div>
        <span class="ed-arrow">→</span>
        <SigilMemoryPicker v-model="form.sigilHash" :options="allSigilOptions" @pick="onPickSigil" :placeholder="t('sigil.memory.selectSigil')" />
        <div class="ed-level" :class="{ maxed: sigilAtMax }">
          <input v-model.number="form.sigilLevel" type="number" min="0" max="999" />
          <span v-if="sigilMax != null" class="ed-level-hint">/ {{ sigilMax }}</span>
        </div>
        <button class="ed-max-btn" :disabled="sigilMax == null || sigilAtMax" @click="maxSigil" :title="sigilMax != null ? t('sigil.memory.levelCap', { max: sigilMax }) : t('sigil.memory.noLevelMetadata')">{{ t('sigil.common.max') }}</button>
      </div>

      <div class="ed-row">
        <span class="ed-label">{{ t('sigil.common.primary') }}</span>
        <div class="ed-current">
          <span class="ed-current-name" :class="{ dim: primaryRead.dim }">{{ primaryRead.text }}</span>
          <span v-if="status.primaryTraitHash" class="ed-current-lv">{{ t('sigil.common.level', { level: status.primaryTraitLevel }) }}</span>
        </div>
        <span class="ed-arrow">→</span>
        <SigilMemoryPicker v-model="form.primaryTraitHash" :options="allTraitOptions" @pick="onPickPrimary" :placeholder="t('sigil.memory.selectPrimary')" />
        <div class="ed-level" :class="{ maxed: primaryAtMax }">
          <input v-model.number="form.primaryTraitLevel" type="number" min="0" max="999" />
          <span v-if="primaryMax != null" class="ed-level-hint">/ {{ primaryMax }}</span>
        </div>
        <button class="ed-max-btn" :disabled="primaryMax == null || primaryAtMax" @click="maxPrimary" :title="primaryMax != null ? t('sigil.memory.levelCap', { max: primaryMax }) : t('sigil.memory.noLevelMetadata')">{{ t('sigil.common.max') }}</button>
      </div>

      <div class="ed-row">
        <span class="ed-label">{{ t('sigil.common.secondary') }}</span>
        <div class="ed-current">
          <span class="ed-current-name" :class="{ dim: secondaryRead.dim }">{{ secondaryRead.text }}</span>
          <span v-if="status.secondaryTraitHash" class="ed-current-lv">{{ t('sigil.common.level', { level: status.secondaryTraitLevel }) }}</span>
        </div>
        <span class="ed-arrow">→</span>
        <SigilMemoryPicker v-model="form.secondaryTraitHash" :options="secondaryTraitOptions" @pick="onPickSecondary" optional :placeholder="t('sigil.memory.selectOptional')" />
        <div class="ed-level" :class="{ maxed: secondaryAtMax }">
          <input v-model.number="form.secondaryTraitLevel" type="number" min="0" max="999" />
          <span v-if="secondaryMax != null" class="ed-level-hint">/ {{ secondaryMax }}</span>
        </div>
        <button class="ed-max-btn" :disabled="secondaryMax == null || secondaryAtMax" @click="maxSecondary" :title="secondaryMax != null ? t('sigil.memory.levelCap', { max: secondaryMax }) : t('sigil.memory.noLevelMetadata')">{{ t('sigil.common.max') }}</button>
      </div>

      <div class="warn-slot">
        <div v-if="warnings.length" class="warn-list">
          <div v-for="(w, i) in warnings" :key="i" class="warn-inline">⚠ {{ w }}</div>
        </div>
      </div>

      <div class="ed-bar">
        <span class="ed-changed">{{ t('sigil.memory.changes', { count: changedCount }) }}{{ changedCount ? t('sigil.memory.notWritten') : '' }}</span>
        <button class="ed-max-all" :disabled="applying || !canOneClickMax" @click="oneClickMax" :title="t('sigil.memory.maxAllTitle')">{{ t('sigil.memory.maxAll') }}</button>
        <button class="ed-write" :disabled="applying || !status.selectedAddr" @click="write">{{ applying ? t('sigil.common.writing') : t('sigil.common.write') }}</button>
      </div>
    </div>

    <!-- Shared list: templates + history -->
    <div class="section">
      <div class="tabs-head">
        <div class="tabs">
          <span class="tab" :class="{ active: tab === 'templates' }" @click="tab = 'templates'">{{ t('sigil.memory.templates') }} <span class="tab-count">{{ templates.length }}</span></span>
          <span class="tab" :class="{ active: tab === 'history' }" @click="tab = 'history'">{{ t('sigil.memory.recent') }} <span class="tab-count">{{ history.length }}</span></span>
        </div>
        <div v-if="tab === 'templates'">
          <input v-model="templateSearch" class="search-input" :placeholder="t('sigil.memory.searchTemplates')" />
        </div>
        <button v-else-if="history.length" class="row-tool" :title="t('sigil.memory.clearRecentTitle')" @click="clearWriteHistory">{{ t('sigil.common.clear') }}</button>
      </div>

      <div v-if="tab === 'templates'">
        <div v-if="!filteredTemplates.length" class="tpl-empty">
          {{ templates.length ? t('sigil.memory.noMatch') : t('sigil.memory.noTemplates') }}
        </div>
        <ul v-else class="row-list">
          <li v-for="template in filteredTemplates" :key="template.id" class="row-item" @click="applyEntry(template)">
            <span class="row-name">
              <template v-if="renamingId === template.id">
                <span class="rename-group" ref="renameEl">
                  <input v-model="renameBuffer" class="rename-input" @click.stop @keydown.enter="confirmRename" @keydown.escape="cancelRename" />
                  <button class="rename-confirm" :disabled="!renameBuffer.trim()" @click.stop="confirmRename" :title="t('sigil.memory.saveTitle')">✓</button>
                  <button class="rename-cancel" @click.stop="cancelRename" :title="t('sigil.memory.cancelTitle')">✕</button>
                </span>
              </template>
              <template v-else>
                <span class="row-name-text">{{ template.name }}</span>
                <span class="row-name-lv">{{ t('sigil.common.level', { level: template.sigilLevel }) }}</span>
              </template>
            </span>
            <span class="row-chip">
              <span class="row-chip-tag">{{ t('sigil.common.primary') }}</span>
              <span class="row-chip-name">{{ nameFor(traitByHash, template.primaryTraitHash, t('sigil.common.none')) }}</span>
              <span class="row-chip-lv">{{ t('sigil.common.level', { level: template.primaryTraitLevel }) }}</span>
            </span>
            <span class="row-chip" :class="{ 'empty-slot': !template.secondaryTraitHash }">
              <span class="row-chip-tag">{{ t('sigil.common.secondary') }}</span>
              <span class="row-chip-name">{{ template.secondaryTraitHash ? nameFor(traitByHash, template.secondaryTraitHash) : t('sigil.common.none') }}</span>
              <span class="row-chip-lv">{{ t('sigil.common.level', { level: template.secondaryTraitLevel }) }}</span>
            </span>
            <span class="row-tools" @click.stop>
              <button class="row-tool" :title="t('sigil.memory.renameTitle')" @click="startRename(template.id, template.name)">✎</button>
              <button class="row-tool" :title="t('sigil.memory.deleteTitle')" @click="deleteTemplate(template.id)">✕</button>
            </span>
            <button class="row-apply" :disabled="!status.selectedAddr || applying" @click.stop="applyAndWrite(template)" :title="t('sigil.memory.applyTitle')">{{ t('sigil.memory.apply') }}</button>
          </li>
        </ul>
      </div>

      <div v-else>
        <div v-if="!history.length" class="tpl-empty">{{ t('sigil.memory.noHistory') }}</div>
        <ul v-else class="row-list">
          <li v-for="h in history" :key="h.id" class="row-item" @click="applyEntry(h)">
            <span class="row-name">
              <span class="row-name-text">{{ nameFor(sigilByHash, h.sigilHash, t('sigil.common.none')) }}</span>
              <span class="row-name-lv">{{ t('sigil.common.level', { level: h.sigilLevel }) }}</span>
            </span>
            <span class="row-chip">
              <span class="row-chip-tag">{{ t('sigil.common.primary') }}</span>
              <span class="row-chip-name">{{ nameFor(traitByHash, h.primaryTraitHash, t('sigil.common.none')) }}</span>
              <span class="row-chip-lv">{{ t('sigil.common.level', { level: h.primaryTraitLevel }) }}</span>
            </span>
            <span class="row-chip" :class="{ 'empty-slot': !h.secondaryTraitHash }">
              <span class="row-chip-tag">{{ t('sigil.common.secondary') }}</span>
              <span class="row-chip-name">{{ h.secondaryTraitHash ? nameFor(traitByHash, h.secondaryTraitHash) : t('sigil.common.none') }}</span>
              <span class="row-chip-lv">{{ t('sigil.common.level', { level: h.secondaryTraitLevel }) }}</span>
            </span>
            <span class="row-meta">{{ fmtRelTime(h.createdAt) }}</span>
            <button class="row-apply" :disabled="!status.selectedAddr || applying" @click.stop="applyAndWrite(h)" :title="t('sigil.memory.applyTitle')">{{ t('sigil.memory.apply') }}</button>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>

<style scoped>
.memory-sigil { width:100%; display:flex; flex-direction:column; gap:14px; font-family:inherit; }
.section { padding:14px 16px; border:1px solid rgba(255,255,255,.08); border-radius:8px; background:rgba(255,255,255,.04); display:flex; flex-direction:column; gap:10px; font-family:inherit; }
.section.muted { opacity:.6; }
.section-title { color:rgba(255,255,255,.4); font-size:.72rem; font-weight:600; letter-spacing:.1em; text-transform:uppercase; }
.hint-inline { font-size:.72rem; color:rgba(255,255,255,.4); }

/* Connection */
.conn-section { padding:10px 14px; }
.conn-row { display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:10px; }
.conn-left { display:flex; gap:10px; align-items:center; flex-wrap:wrap; }
.conn-right { display:flex; gap:8px; align-items:center; }
.chip { padding:3px 10px; border:1px solid rgba(255,255,255,.12); border-radius:999px; background:rgba(255,255,255,.05); font-size:.72rem; color:rgba(255,255,255,.55); font-family:inherit; }
.chip.state { color:#4ade80; border-color:rgba(74,222,128,.3); background:rgba(74,222,128,.06); }
.chip.dim { color:rgba(255,255,255,.4); }

/* Button base */
.btn { padding:8px 14px; border:1px solid rgba(255,255,255,.16); border-radius:6px; background:rgba(255,255,255,.06); color:rgba(255,255,255,.75); font-size:.8rem; font-weight:600; cursor:pointer; font-family:inherit; }
.btn:disabled { opacity:.4; cursor:not-allowed; }
.btn-cyan { border-color:rgba(103,232,249,.35); color:#67e8f9; background:rgba(103,232,249,.1); }
.btn.tiny { padding:4px 9px; font-size:.7rem; }

/* Editor header */
.editor-header { display:flex; justify-content:space-between; align-items:center; margin-bottom:2px; }
.editor-actions { display:flex; gap:14px; }
.ed-link { color:rgba(255,255,255,.5); font-size:.72rem; cursor:pointer; background:none; border:0; font-family:inherit; padding:0; }
.ed-link:hover:not(:disabled) { color:#67e8f9; }
.ed-link:disabled { opacity:.35; cursor:not-allowed; }

/* Editor row: [label | current | arrow | picker | level | max] */
.ed-row { display:grid; grid-template-columns:36px 1fr 22px 1fr 82px 50px; column-gap:8px; align-items:center; padding:4px 0; }
.ed-label { color:rgba(255,255,255,.4); font-size:.72rem; font-weight:500; }
.ed-current { display:flex; align-items:baseline; gap:6px; overflow:hidden; min-width:0; }
.ed-current-name { color:rgba(255,255,255,.85); font-weight:600; font-size:.82rem; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ed-current-name.dim { color:rgba(255,255,255,.35); font-weight:400; }
.ed-current-lv { color:rgba(255,255,255,.4); font-size:.68rem; font-family:ui-monospace,Consolas,monospace; flex-shrink:0; }
.ed-arrow { color:rgba(103,232,249,.35); font-size:.9rem; text-align:center; }

/* Contained level box: input on left, /max hint on right */
.ed-level { display:flex; align-items:baseline; gap:3px; padding:6px 10px; border:1px solid rgba(255,255,255,.14); border-radius:6px; background:rgba(255,255,255,.05); justify-content:flex-end; }
.ed-level:focus-within { border-color:rgba(103,232,249,.35); }
.ed-level.maxed { border-color:rgba(74,222,128,.4); background:rgba(74,222,128,.06); }
.ed-level input { border:none; background:transparent; width:24px; padding:0; color:rgba(255,255,255,.92); font:700 .84rem "Nunito",sans-serif; font-variant-numeric:tabular-nums; text-align:right; outline:none; }
.ed-level.maxed input { color:#4ade80; }
.ed-level-hint { color:rgba(255,255,255,.35); font-size:.68rem; font-family:ui-monospace,Consolas,monospace; }
.ed-level.maxed .ed-level-hint { color:#4ade80; }
.ed-level input[type=number]::-webkit-inner-spin-button,
.ed-level input[type=number]::-webkit-outer-spin-button { -webkit-appearance:none; margin:0; }
.ed-level input[type=number] { -moz-appearance:textfield; }

/* MAX button — small */
.ed-max-btn { padding:5px 8px; border:1px solid rgba(103,232,249,.35); background:rgba(103,232,249,.1); color:#67e8f9; border-radius:5px; font:700 .66rem "Nunito",sans-serif; cursor:pointer; letter-spacing:.02em; font-family:inherit; }
.ed-max-btn:hover:not(:disabled) { background:rgba(103,232,249,.18); }
.ed-max-btn:disabled { opacity:.3; cursor:not-allowed; }

/* Warnings + write bar */
.warn-slot { min-height:16px; padding-left:44px; }
.warn-list { display:flex; flex-direction:column; gap:2px; }
.warn-inline { color:#fbbf24; font-size:.7rem; }
.ed-bar { display:flex; justify-content:flex-end; align-items:center; gap:16px; padding-top:10px; margin-top:4px; border-top:1px solid rgba(255,255,255,.06); }
.ed-changed { color:rgba(255,255,255,.4); font-size:.72rem; margin-right:auto; }
.ed-max-all { color:#67e8f9; background:none; border:0; font:600 .74rem "Nunito",sans-serif; cursor:pointer; padding:6px 0; font-family:inherit; }
.ed-max-all:hover:not(:disabled) { text-decoration:underline; }
.ed-max-all:disabled { opacity:.35; cursor:not-allowed; text-decoration:none; }
.ed-write { background:rgba(74,222,128,.14); border:1px solid rgba(74,222,128,.4); color:#4ade80; border-radius:6px; padding:7px 22px; font:700 .78rem "Nunito",sans-serif; cursor:pointer; letter-spacing:.02em; font-family:inherit; }
.ed-write:hover:not(:disabled) { background:rgba(74,222,128,.22); }
.ed-write:disabled { opacity:.4; cursor:not-allowed; }

/* Tabs */
.tabs-head { display:flex; justify-content:space-between; align-items:center; gap:14px; margin-bottom:8px; }
.tabs { display:flex; gap:18px; }
.tab { color:rgba(255,255,255,.4); font-size:.75rem; font-weight:600; cursor:pointer; padding-bottom:6px; border-bottom:2px solid transparent; }
.tab.active { color:rgba(255,255,255,.9); border-bottom-color:#67e8f9; }
.tab-count { color:rgba(255,255,255,.3); font-weight:400; margin-left:4px; }
.search-input { padding:5px 10px; border:1px solid rgba(255,255,255,.12); border-radius:6px; background:rgba(255,255,255,.05); color:#fff; font:inherit; font-size:.72rem; width:170px; font-family:inherit; }

/* Shared list rows — subgrid.
   Columns: name · primary chip · secondary chip · meta/tools (right) · apply (right edge).
   The `1fr` filler pushes meta/tools + apply to the right edge. */
.row-list { list-style:none; margin:0; padding:0; display:grid; grid-template-columns:auto auto auto 1fr auto auto; column-gap:14px; row-gap:2px; }
.row-item { display:grid; grid-template-columns:subgrid; grid-column:1 / -1; align-items:center; padding:7px 10px; border-radius:5px; cursor:pointer; }
.row-item:hover { background:rgba(103,232,249,.05); }

.row-name { display:inline-flex; align-items:baseline; gap:6px; overflow:hidden; min-width:0; }
.row-name-text { color:rgba(255,255,255,.88); font-weight:600; font-size:.8rem; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.row-name-lv { color:rgba(255,255,255,.4); font-size:.66rem; font-family:ui-monospace,Consolas,monospace; flex-shrink:0; }
.rename-group { display:inline-flex; align-items:center; gap:4px; min-width:0; flex:1; }
.rename-input { padding:2px 6px; border:1px solid rgba(103,232,249,.4); border-radius:4px; background:rgba(103,232,249,.06); color:#fff; font:inherit; font-size:.78rem; font-weight:600; min-width:0; flex:1; box-sizing:border-box; outline:none; font-family:inherit; }
.rename-confirm, .rename-cancel { flex-shrink:0; background:transparent; border:1px solid transparent; padding:2px 7px; cursor:pointer; font-size:.7rem; border-radius:3px; font-family:inherit; line-height:1; }
.rename-confirm { color:#4ade80; border-color:rgba(74,222,128,.35); background:rgba(74,222,128,.08); }
.rename-confirm:hover:not(:disabled) { background:rgba(74,222,128,.18); }
.rename-confirm:disabled { opacity:.35; cursor:not-allowed; }
.rename-cancel { color:rgba(255,255,255,.5); }
.rename-cancel:hover { color:#f87171; background:rgba(248,113,113,.1); border-color:rgba(248,113,113,.3); }

.row-chip { display:inline-flex; align-items:baseline; gap:5px; padding:2px 8px; border:1px solid rgba(255,255,255,.08); border-radius:4px; background:rgba(255,255,255,.04); font-size:.72rem; max-width:200px; }
.row-chip-tag { color:rgba(255,255,255,.35); font-size:.62rem; letter-spacing:.05em; font-weight:600; flex-shrink:0; }
.row-chip-name { color:rgba(255,255,255,.78); font-weight:500; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.row-chip-lv { color:rgba(255,255,255,.42); font-size:.64rem; font-family:ui-monospace,Consolas,monospace; flex-shrink:0; }
.row-chip.empty-slot { visibility:hidden; }

.row-meta { color:rgba(255,255,255,.35); font-size:.68rem; justify-self:end; white-space:nowrap; }
.row-tools { display:flex; gap:4px; opacity:0; transition:opacity .12s; justify-self:end; }
.row-item:hover .row-tools { opacity:1; }
.row-tool { background:transparent; border:1px solid transparent; color:rgba(255,255,255,.5); padding:3px 7px; cursor:pointer; font-size:.7rem; border-radius:4px; font-family:inherit; line-height:1; }
.row-tool:hover { color:#67e8f9; background:rgba(103,232,249,.1); border-color:rgba(103,232,249,.3); }

.row-apply { padding:4px 12px; border:1px solid rgba(74,222,128,.35); background:rgba(74,222,128,.1); color:#4ade80; border-radius:5px; font:600 .7rem "Nunito",sans-serif; cursor:pointer; letter-spacing:.02em; font-family:inherit; }
.row-apply:hover:not(:disabled) { background:rgba(74,222,128,.2); }
.row-apply:disabled { opacity:.35; cursor:not-allowed; }

.tpl-empty { padding:22px; text-align:center; color:rgba(255,255,255,.3); font-size:.75rem; }
</style>
