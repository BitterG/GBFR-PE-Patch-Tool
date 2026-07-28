<script setup>
import { ref, reactive, computed, watch, onMounted } from 'vue'
import { GetLastSavePath, SetLastSavePath } from '../../wailsjs/go/main/App'
import { GetWrightstoneList, GetTraitList, GetTraitLevels, GetDefaultTrait,
         LoadSaveFile, GetQueue, AddToQueue, RemoveFromQueue, ClearQueue,
         ApplyQueue, ApplyItems, FileExists, SelectWrightstoneInputSave,
         SelectWrightstoneOutputSave } from '../../wailsjs/go/main/WrightstoneGen'
import { matchText } from '../utils/matchText.js'
import { translate as t } from '../i18n'

const emit = defineEmits(['status'])
function showStatus(msg, type) { emit('status', msg, type) }

const wrightstones = ref([])
const traits = ref([])
const saveLoaded = ref(false)
const saveInfo = reactive({ path: '', occupiedWrightstones: 0, maxSlotId: 0 })
const isApplying = ref(false)
const inPlaceEdit = ref(false)
const applyFlash = ref(false)
let applyFlashTimer = 0

const inputPath = ref('')
const outputPath = ref('')
const selectedWrightstoneID = ref('')
const wrightstoneSearch = ref('')
const selectedTraits = reactive([
  { id: '', level: 0, levels: [] },
  { id: '', level: 0, levels: [] },
  { id: '', level: 0, levels: [] },
])
const quantity = ref(1)
const queue = ref([])

const dataLoading = ref(true)
const dataError = ref('')
const traitSearches = ref(['', '', ''])

const filteredWrightstones = computed(() => {
  const q = wrightstoneSearch.value
  if (!q) return wrightstones.value
  return wrightstones.value.filter(w => matchText(w.displayName, q))
})

const currentSelectionValid = computed(() => {
  return !!selectedWrightstoneID.value && selectedTraits.every(t => !!t.id && !!t.level) && quantity.value > 0
})
const canApply = computed(() => saveLoaded.value && !!outputPath.value.trim() && (queue.value.length > 0 || currentSelectionValid.value))

function filteredTraits(slot) {
  const q = traitSearches.value[slot]
  if (!q) return traits.value
  return traits.value.filter(t => matchText(t.displayName, q))
}

onMounted(async () => {
  try {
    wrightstones.value = await GetWrightstoneList()
    traits.value = await GetTraitList()
    if (!wrightstones.value.length || !traits.value.length) {
      dataError.value = t('wrightstone.generator.errors.emptyData')
    }
    const lastPath = await GetLastSavePath()
    if (lastPath) {
      inputPath.value = lastPath
      outputPath.value = defaultOutputPath(lastPath)
    }
  } catch (e) {
    dataError.value = t('wrightstone.generator.errors.loadData', { error: String(e) })
  } finally {
    dataLoading.value = false
  }
})

function defaultOutputPath(path) {
  if (!path) return ''
  if (/\.dat$/i.test(path)) return path.replace(/(\.dat)$/i, '_wrightstones.dat')
  return `${path}_wrightstones.dat`
}

watch(inPlaceEdit, (enabled) => {
  if (enabled) {
    outputPath.value = inputPath.value.trim()
  } else if (outputPath.value.trim() === inputPath.value.trim()) {
    outputPath.value = defaultOutputPath(inputPath.value.trim())
  }
})

async function browseInput() {
  try {
    const path = await SelectWrightstoneInputSave()
    if (!path) return
    inputPath.value = path
    await loadSave()
  } catch (e) { showStatus(String(e), 'error') }
}

async function browseOutput() {
  try {
    const path = await SelectWrightstoneOutputSave(outputPath.value.trim() || defaultOutputPath(inputPath.value.trim()))
    if (path) outputPath.value = path
  } catch (e) { showStatus(String(e), 'error') }
}

async function loadSave() {
  if (!inputPath.value.trim()) { showStatus(t('wrightstone.generator.errors.enterSavePath'), 'error'); return }
  try {
    const info = await LoadSaveFile(inputPath.value.trim())
    Object.assign(saveInfo, info)
    saveLoaded.value = true
    outputPath.value = inPlaceEdit.value ? info.path : defaultOutputPath(info.path)
    await SetLastSavePath(info.path)
    showStatus(t('wrightstone.generator.status.saveLoaded', { count: info.occupiedWrightstones }), 'success')
  } catch (e) {
    showStatus(String(e), 'error')
  }
}

watch(selectedWrightstoneID, async (id) => {
  if (!id) return
  const w = wrightstones.value.find(x => x.internalId === id)
  if (w) wrightstoneSearch.value = w.displayName
  try {
    const def = await GetDefaultTrait(id)
    if (def) {
      selectedTraits[0].id = def.internalId
      traitSearches.value[0] = def.displayName
      await loadTraitLevels(0)
    }
  } catch (e) { showStatus(String(e), 'error') }
})

async function loadTraitLevels(slot) {
  const traitID = selectedTraits[slot].id
  if (!traitID) {
    selectedTraits[slot].levels = []
    selectedTraits[slot].level = 0
    return
  }
  const trait = traits.value.find(t => t.internalId === traitID)
  if (trait) traitSearches.value[slot] = trait.displayName
  try {
    const levels = await GetTraitLevels(traitID)
    selectedTraits[slot].levels = levels
    selectedTraits[slot].level = levels[levels.length - 1] || 0
  } catch (e) {
    selectedTraits[slot].levels = []
    selectedTraits[slot].level = 0
    showStatus(String(e), 'error')
  }
}

function traitLabel(slot) {
  return t(`wrightstone.generator.traits.slot.${['first', 'second', 'third'][slot]}`)
}

function buildCurrentItem() {
  return {
    wrightstoneId: selectedWrightstoneID.value,
    wrightstoneName: '',
    firstTraitId: selectedTraits[0].id,
    firstTraitName: '',
    firstLevel: selectedTraits[0].level,
    secondTraitId: selectedTraits[1].id,
    secondTraitName: '',
    secondLevel: selectedTraits[1].level,
    thirdTraitId: selectedTraits[2].id,
    thirdTraitName: '',
    thirdLevel: selectedTraits[2].level,
    quantity: quantity.value,
  }
}

function validateCurrentSelection() {
  if (!selectedWrightstoneID.value) { showStatus(t('wrightstone.generator.errors.selectWrightstone'), 'error'); return false }
  for (let i = 0; i < 3; i++) {
    if (!selectedTraits[i].id || !selectedTraits[i].level) {
      showStatus(t('wrightstone.generator.errors.selectTraitAndLevel', { trait: traitLabel(i) }), 'error')
      return false
    }
  }
  if (!quantity.value || quantity.value < 1) { showStatus(t('wrightstone.generator.errors.quantityMinimum'), 'error'); return false }
  return true
}

async function addToQueue() {
  if (!validateCurrentSelection()) return
  try {
    await AddToQueue(buildCurrentItem())
    queue.value = await GetQueue()
    showStatus(t('wrightstone.generator.status.addedToQueue'), 'success')
  } catch (e) { showStatus(String(e), 'error') }
}

async function removeFromQueue(index) {
  try {
    await RemoveFromQueue(index)
    queue.value = await GetQueue()
  } catch (e) { showStatus(String(e), 'error') }
}

async function clearQueueAll() {
  await ClearQueue()
  queue.value = []
}

function flashApplySuccess() {
  applyFlash.value = false
  clearTimeout(applyFlashTimer)
  requestAnimationFrame(() => {
    applyFlash.value = true
    applyFlashTimer = window.setTimeout(() => { applyFlash.value = false }, 900)
  })
}

async function applyQueueToSave() {
  if (!saveLoaded.value) { showStatus(t('wrightstone.generator.errors.loadSaveFirst'), 'error'); return }
  if (!outputPath.value.trim()) { showStatus(t('wrightstone.generator.errors.enterOutputPath'), 'error'); return }
  if (!queue.value.length && !validateCurrentSelection()) return

  isApplying.value = true
  try {
    const output = outputPath.value.trim()
    const exists = await FileExists(output)
    if (exists && !window.confirm(t('wrightstone.generator.confirm.overwriteOutput', { path: output }))) return

    const result = queue.value.length
      ? await ApplyQueue(output)
      : await ApplyItems([buildCurrentItem()], output)
    queue.value = []
    if (inPlaceEdit.value) await loadSave()
    flashApplySuccess()
    showStatus(t('wrightstone.generator.status.written', { created: result.createdCount, verified: result.verifiedCount }), 'success')
  } catch (e) { showStatus(String(e), 'error') }
  finally { isApplying.value = false }
}
</script>

<template>
  <div class="wrightstone-container">
    <div class="section">
      <div class="section-title">{{ t('wrightstone.generator.saveFile.title') }}</div>
      <div class="input-row">
        <input v-model="inputPath" type="text" class="text-input flex-1" :placeholder="t('wrightstone.generator.saveFile.inputPlaceholder')" />
        <button class="btn-action btn-cyan" @click="browseInput">{{ t('wrightstone.generator.actions.browse') }}</button>
        <button class="btn-action btn-green" @click="loadSave">{{ t('wrightstone.generator.actions.load') }}</button>
      </div>
      <div v-if="saveLoaded" class="save-info">
        {{ t('wrightstone.generator.saveFile.loadedInfo', { count: saveInfo.occupiedWrightstones, slot: saveInfo.maxSlotId }) }}
      </div>
    </div>

    <div class="section">
      <div class="section-title">
        {{ t('wrightstone.generator.configuration.title') }}
        <span class="info-dot" :title="t('wrightstone.generator.configuration.help')">!</span>
      </div>
      <div v-if="dataError" class="data-error">{{ dataError }}</div>
      <div class="field">
        <label>{{ t('wrightstone.generator.fields.wrightstone') }} {{ dataLoading ? t('wrightstone.generator.loadingSuffix') : '' }}</label>
        <input v-model="wrightstoneSearch" type="text" class="text-input" :placeholder="t('wrightstone.generator.placeholders.filter')" />
        <select v-model="selectedWrightstoneID" class="select-input" size="5">
          <option value="">{{ t('wrightstone.generator.options.selectWrightstone') }}</option>
          <option v-for="w in filteredWrightstones" :key="w.internalId" :value="w.internalId">
            {{ w.displayName }}<template v-if="w.defaultTraitName">{{ t('wrightstone.generator.options.defaultTrait', { trait: w.defaultTraitName }) }}</template>
          </option>
        </select>
      </div>

      <div v-for="(_, i) in selectedTraits" :key="i" class="trait-card">
        <div class="field flex-1">
          <label>{{ traitLabel(i) }}</label>
          <input v-model="traitSearches[i]" type="text" class="text-input" :placeholder="t('wrightstone.generator.placeholders.filterTraits')" />
          <select v-model="selectedTraits[i].id" class="select-input" size="4" @change="loadTraitLevels(i)">
            <option value="">{{ t('wrightstone.generator.options.selectTrait') }}</option>
            <option v-for="trait in filteredTraits(i)" :key="trait.internalId" :value="trait.internalId">
              {{ t('wrightstone.generator.options.traitMaxLevel', { trait: trait.displayName, level: trait.maxLevel }) }}
            </option>
          </select>
        </div>
        <div class="field level-field">
          <label>{{ t('wrightstone.generator.fields.level') }}</label>
          <select v-model="selectedTraits[i].level" class="select-input" :disabled="!selectedTraits[i].levels.length">
            <option v-for="l in selectedTraits[i].levels" :key="l" :value="l">{{ t('wrightstone.generator.level', { level: l }) }}</option>
          </select>
        </div>
      </div>

      <div class="input-row">
        <div class="field flex-1">
          <label>{{ t('wrightstone.generator.fields.quantity') }}</label>
          <input v-model.number="quantity" type="number" min="1" max="999" class="text-input" />
        </div>
        <button class="btn-action btn-purple add-btn" @click="addToQueue" :disabled="!selectedWrightstoneID">
          {{ t('wrightstone.generator.actions.addToQueue') }}
        </button>
      </div>
    </div>

    <div class="section">
      <div class="section-title">
        {{ t('wrightstone.generator.queue.title', { count: queue.length }) }}
        <button v-if="queue.length" class="btn-link" @click="clearQueueAll">{{ t('wrightstone.generator.actions.clear') }}</button>
      </div>
      <div v-if="!queue.length" class="empty-hint">{{ t('wrightstone.generator.queue.empty') }}</div>
      <div v-else class="queue-list">
        <div v-for="(item, i) in queue" :key="i" class="queue-item">
          <div class="queue-info">
            <span class="queue-name">{{ item.wrightstoneName }}</span>
            <span class="queue-detail">
              {{ t('wrightstone.generator.queue.itemDetail', { firstTrait: item.firstTraitName, firstLevel: item.firstLevel, secondTrait: item.secondTraitName, secondLevel: item.secondLevel, thirdTrait: item.thirdTraitName, thirdLevel: item.thirdLevel, quantity: item.quantity }) }}
            </span>
          </div>
          <button class="btn-icon" @click="removeFromQueue(i)" :title="t('wrightstone.generator.actions.remove')">✕</button>
        </div>
      </div>
    </div>

    <div class="section apply-section" :class="{ 'apply-flash': applyFlash }">
      <div class="section-title">{{ t('wrightstone.generator.output.title') }}</div>
      <div class="input-row">
        <input v-model="outputPath" type="text" class="text-input flex-1" :class="{ 'danger-path': inPlaceEdit }"
          :readonly="inPlaceEdit" :placeholder="t('wrightstone.generator.output.placeholder')" />
        <button class="btn-action btn-cyan" @click="browseOutput" :disabled="inPlaceEdit">{{ t('wrightstone.generator.actions.browse') }}</button>
        <button class="btn-action btn-cyan" @click="applyQueueToSave" :disabled="isApplying || !canApply">
          {{ isApplying ? t('wrightstone.generator.actions.writing') : t('wrightstone.generator.actions.apply') }}
        </button>
      </div>
      <label class="toggle-row">
        <input v-model="inPlaceEdit" type="checkbox" />
        <span>{{ t('wrightstone.generator.output.inPlaceEdit') }}</span>
      </label>
      <div v-if="inPlaceEdit" class="danger-hint">{{ t('wrightstone.generator.output.inPlaceWarning') }}</div>
      <div v-else class="warning-hint">{{ t('wrightstone.generator.output.safeWriteHint') }}</div>
    </div>
  </div>
</template>

<style scoped>
.wrightstone-container { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.section { border-radius: 12px; padding: 14px 16px; background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.06); display: flex; flex-direction: column; gap: 10px; }
.apply-section { position: relative; overflow: hidden; z-index: 0; transition: border-color 0.3s, box-shadow 0.3s; }
.apply-section > * { position: relative; z-index: 1; }
.apply-section::after { content: ""; position: absolute; inset: 0; z-index: 0; border-radius: 12px; background: #abd373; transform: translateY(calc(-100% - 2px)); transition: transform 0.5s ease; }
.apply-section.apply-flash { border-color: rgba(171,211,115,0.55); box-shadow: 0 14px 34px rgba(171,211,115,0.18); }
.apply-section.apply-flash::after { transform: translateY(0); }
.apply-section.apply-flash .section-title,
.apply-section.apply-flash .toggle-row { color: #1f2937; }
.apply-section.apply-flash .text-input { border-color: rgba(31,41,55,0.22); background: rgba(255,255,255,0.22); color: #1f2937; }
.apply-section.apply-flash .btn-cyan { border-color: rgba(31,41,55,0.22); background: rgba(31,41,55,0.12); color: #1f2937; }
.apply-section.apply-flash .warning-hint,
.apply-section.apply-flash .danger-hint { border-color: rgba(31,41,55,0.18); background: rgba(255,255,255,0.18); color: rgba(31,41,55,0.78); }
.section-title { font-size: 0.78rem; font-weight: 600; color: rgba(255,255,255,0.35); letter-spacing: 1px; display: flex; align-items: center; justify-content: space-between; }
.info-dot { display: inline-flex; align-items: center; justify-content: center; width: 15px; height: 15px; border-radius: 50%; border: 1px solid rgba(103,232,249,0.35); color: #67e8f9; background: rgba(103,232,249,0.08); font-size: 0.68rem; font-weight: 700; cursor: help; letter-spacing: 0; }
.field { display: flex; flex-direction: column; gap: 4px; }
.field label { font-size: 0.7rem; color: rgba(255,255,255,0.3); }
.text-input, .select-input { padding: 8px 12px; border-radius: 8px; border: 1px solid rgba(255,255,255,0.12); background: rgba(255,255,255,0.06); color: #fff; font-size: 0.82rem; font-family: inherit; outline: none; transition: border-color 0.2s; box-sizing: border-box; }
.select-input { background-color: transparent; scrollbar-width: thin; scrollbar-color: rgba(255,255,255,0.15) rgba(0,0,0,0.2); }
.select-input::-webkit-scrollbar { width: 6px; }
.select-input::-webkit-scrollbar-track { background: rgba(0,0,0,0.2); border-radius: 3px; }
.select-input::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.15); border-radius: 3px; }
.select-input::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,0.25); }
.select-input option { background: #1b2636; color: #fff; }
.text-input:focus { border-color: rgba(103,232,249,0.4); background: rgba(255,255,255,0.1); }
.select-input:focus { border-color: rgba(103,232,249,0.4); background: transparent; }
.select-input:disabled { opacity: 0.4; cursor: not-allowed; }
.input-row { display: flex; gap: 8px; align-items: flex-end; }
.flex-1 { flex: 1; }
.trait-card { display: flex; gap: 10px; align-items: flex-end; padding: 10px; border-radius: 10px; background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.05); }
.level-field { width: 120px; flex-shrink: 0; }
.btn-action { padding: 8px 16px; border-radius: 8px; border: none; font-size: 0.8rem; font-weight: 600; cursor: pointer; white-space: nowrap; transition: transform 0.15s, opacity 0.2s; }
.btn-action:not(:disabled):hover { transform: scale(1.03); }
.btn-action:disabled { opacity: 0.35; cursor: not-allowed; }
.btn-green { background: rgba(34,197,94,0.18); color: #4ade80; border: 1px solid rgba(34,197,94,0.3); }
.btn-purple { background: rgba(165,180,252,0.15); color: #a5b4fc; border: 1px solid rgba(165,180,252,0.3); }
.btn-cyan { background: rgba(103,232,249,0.15); color: #67e8f9; border: 1px solid rgba(103,232,249,0.3); }
.add-btn { padding-top: 8px; padding-bottom: 8px; align-self: flex-end; }
.btn-link { background: none; border: none; color: rgba(255,255,255,0.3); font-size: 0.72rem; cursor: pointer; padding: 0 4px; }
.btn-link:hover { color: rgba(239,68,68,0.7); }
.btn-icon { background: none; border: none; color: rgba(255,255,255,0.3); cursor: pointer; font-size: 0.85rem; padding: 2px 6px; border-radius: 4px; transition: color 0.15s; }
.btn-icon:hover { color: #f87171; }
.save-info { font-size: 0.72rem; color: rgba(74,222,128,0.6); }
.empty-hint { font-size: 0.75rem; color: rgba(255,255,255,0.2); text-align: center; padding: 8px 0; }
.warning-hint { font-size: 0.72rem; color: rgba(251,191,36,0.8); }
.toggle-row { display: flex; align-items: center; gap: 8px; font-size: 0.78rem; color: rgba(255,255,255,0.75); }
.danger-hint { font-size: 0.72rem; color: rgba(248,113,113,0.95); padding: 8px 12px; background: rgba(239,68,68,0.08); border: 1px solid rgba(239,68,68,0.2); border-radius: 6px; line-height: 1.5; }
.danger-path { background: rgba(239,68,68,0.14) !important; border-color: rgba(239,68,68,0.55) !important; color: #fecaca; }
.data-error { font-size: 0.75rem; color: #f87171; }
.queue-list { display: flex; flex-direction: column; gap: 6px; }
.queue-item { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 10px 12px; border-radius: 10px; background: rgba(255,255,255,0.05); border: 1px solid rgba(255,255,255,0.06); }
.queue-info { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.queue-name { color: rgba(255,255,255,0.7); font-size: 0.84rem; font-weight: 600; }
.queue-detail { color: rgba(255,255,255,0.35); font-size: 0.72rem; }
</style>
