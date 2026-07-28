<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { translate as t } from '../i18n'
import { SigilMemoryEnable, SigilMemoryGetOptions, SigilMemoryGetStatus, SigilMemoryUpdate, SelectLogsSigilLoadouts } from '../../wailsjs/go/main/App'

const emit = defineEmits(['status'])
const MAX_ENTRIES = 12
const FORMAT = 'gbfr-sigil-loadout'
const VERSION = 1

const options = reactive({ sigils: [], traits: [] })
const entries = ref([])
const mode = ref('idle')
const busy = ref(false)
const fileInput = ref(null)
const writeIndex = ref(0)
const exportVersion = ref('')
const exportComment = ref('')
const importedVersion = ref('')
const importedComment = ref('')
const importWarnings = ref([])
const sharedLoadout = ref(null)
const logsLoadouts = ref([])
const logsRecords = ref([])
const selectedLogsLoadout = ref('')
const selectedLogsRecord = ref('')
const logsDialogOpen = ref(false)
let timer = 0
let lastSeen = ''

function show(message, type) { emit('status', message, type) }
function hex(value) { return `0x${(Number(value) >>> 0).toString(16).toUpperCase().padStart(8, '0')}` }
function snapshot(status) {
  return {
    sigilHash: status.sigilHash >>> 0,
    sigilLevel: status.sigilLevel >>> 0,
    primaryTraitHash: status.primaryTraitHash >>> 0,
    primaryTraitLevel: status.primaryTraitLevel >>> 0,
    secondaryTraitHash: status.secondaryTraitHash >>> 0,
    secondaryTraitLevel: status.secondaryTraitLevel >>> 0,
  }
}
function entryKey(entry) {
  return [entry.sigilHash, entry.sigilLevel, entry.primaryTraitHash, entry.primaryTraitLevel,
    entry.secondaryTraitHash, entry.secondaryTraitLevel].join(':')
}
function validEntry(value) {
  if (!value || typeof value !== 'object') return false
  return ['sigilHash', 'sigilLevel', 'primaryTraitHash', 'primaryTraitLevel', 'secondaryTraitHash', 'secondaryTraitLevel']
    .every(key => Number.isInteger(value[key]) && value[key] >= 0 && value[key] <= 0xFFFFFFFF)
}

function validCompleteLoadout(data) {
  if (!data || data.format !== 'gbfr-loadout' || !Number.isInteger(data.version) || data.version < 1 || data.version > 10 || !Array.isArray(data.sigils)) return false
  const hash = value => typeof value === 'string' && /^(?:0x)?[0-9a-f]{1,8}$/i.test(value)
  const level = value => Number.isInteger(value) && value >= 0
  const indexes = new Set()
  if (data.sigils.length > MAX_ENTRIES || !data.sigils.every(item => {
    if (!item || !level(item.level) || !level(item.primaryTraitLevel) || !hash(item.hash) || !hash(item.primaryTraitHash) || (item.secondaryTraitHash && !hash(item.secondaryTraitHash))) return false
    if (data.version >= 2 && (!Number.isInteger(item.index) || item.index < 0 || item.index >= MAX_ENTRIES || indexes.has(item.index))) return false
    indexes.add(item.index); return true
  })) return false
  if (Array.isArray(data.summons) && (data.summons.length !== 4 || !data.summons.every(item => item && hash(item.typeHash) && hash(item.mainTraitHash) && hash(item.subParamHash) && level(item.mainTraitLevel) && level(item.subParamLevel) && level(item.rank)))) return false
  if ((data.skills?.length || 0) > 4 || !data.skills?.every?.(item => item && hash(item.hash))) return false
  if ((data.masteryHashes?.length || 0) > 50 || !data.masteryHashes?.every?.(hash)) return false
  if (data.weaponSkillHashes && (!Array.isArray(data.weaponSkillHashes) || !data.weaponSkillHashes.every(hash))) return false
  if (data.version >= 4 && (!Array.isArray(data.overLimit) || data.overLimit.length !== 4 || !data.overLimit.every((item, index) => item && item.index === index && level(item.level) && (!item.attributeHash || hash(item.attributeHash))))) return false
  if (data.version >= 5 && (!Array.isArray(data.weaponSkillHashes) || data.weaponSkillHashes.length !== 5)) return false
  return true
}

const sigilNames = computed(() => new Map(options.sigils.map(item => [item.hash >>> 0, item.displayName])))
const traitNames = computed(() => new Map(options.traits.map(item => [item.hash >>> 0, item.displayName])))
const modeText = computed(() => ({
  idle: t('sigil.loadout.idle'),
  record: t('sigil.loadout.recording', { count: entries.value.length, max: MAX_ENTRIES }),
  write: t('sigil.loadout.writing', { count: writeIndex.value, total: entries.value.length }),
}[mode.value]))
function nameFor(map, value) { return map.get(value >>> 0) || t('sigil.loadout.unknown', { hash: hex(value) }) }
function detail(entry) {
  return t('sigil.loadout.entryDetails', {
    sigil: nameFor(sigilNames.value, entry.sigilHash),
    sigilLevel: t('sigil.common.level', { level: entry.sigilLevel }),
    primary: nameFor(traitNames.value, entry.primaryTraitHash),
    primaryLevel: t('sigil.common.level', { level: entry.primaryTraitLevel }),
    secondary: entry.secondaryTraitHash ? t('sigil.loadout.secondaryDetails', { name: nameFor(traitNames.value, entry.secondaryTraitHash), level: t('sigil.common.level', { level: entry.secondaryTraitLevel }) }) : '',
  })
}

async function enableReader() { await SigilMemoryEnable() }
function stop() {
  if (timer) window.clearInterval(timer)
  timer = 0
  mode.value = 'idle'
  lastSeen = ''
}

async function poll() {
  if (busy.value || mode.value === 'idle') return
  busy.value = true
  try {
    const status = await SigilMemoryGetStatus()
    if (!status.hooked || !status.selectedAddr || !status.sigilHash) return
    const current = snapshot(status)
    const key = entryKey(current)
    if (key === lastSeen) return
    lastSeen = key

    if (mode.value === 'record') {
      entries.value = [...entries.value, current]
      show(t('sigil.loadout.recorded', { count: entries.value.length, max: MAX_ENTRIES, name: nameFor(sigilNames.value, current.sigilHash) }), 'success')
      if (entries.value.length === MAX_ENTRIES) {
        stop()
        show(t('sigil.loadout.recordComplete'), 'success')
      }
      return
    }

    if (mode.value === 'write' && writeIndex.value < entries.value.length) {
      const target = entries.value[writeIndex.value]
      await SigilMemoryUpdate(target)
      writeIndex.value++
      show(t('sigil.loadout.writingNamed', { count: writeIndex.value, total: entries.value.length, name: nameFor(sigilNames.value, target.sigilHash) }), 'success')
      lastSeen = ''
      if (writeIndex.value === entries.value.length) {
        stop()
        show(t('sigil.loadout.writeComplete'), 'success')
      }
    }
  } catch (error) {
    stop()
    show(String(error), 'error')
  } finally {
    busy.value = false
  }
}

async function startRecord() {
  if (mode.value !== 'idle') return
  try {
    await enableReader()
    entries.value = []
    sharedLoadout.value = null
    importWarnings.value = []
    writeIndex.value = 0
    lastSeen = ''
    mode.value = 'record'
    await poll()
    timer = window.setInterval(poll, 50)
    show(t('sigil.loadout.startRecord'), 'success')
  } catch (error) { show(String(error), 'error') }
}

async function startWrite() {
  if (mode.value !== 'idle') return
  if (!entries.value.length) { show(t('sigil.loadout.needLoadout'), 'error'); return }
  try {
    await enableReader()
    writeIndex.value = 0
    lastSeen = ''
    mode.value = 'write'
    timer = window.setInterval(poll, 50)
    show(t('sigil.loadout.startWrite'), 'success')
  } catch (error) { show(String(error), 'error') }
}

function clearEntries() {
  if (mode.value !== 'idle') return
  entries.value = []
  sharedLoadout.value = null
  importWarnings.value = []
  writeIndex.value = 0
}

function selectLogsRecord() {
  const record = logsRecords.value[Number(selectedLogsRecord.value)]
  logsLoadouts.value = record?.loadouts || []
  selectedLogsLoadout.value = '0'
  if (logsLoadouts.value.length) selectLogsLoadout()
}

function selectLogsLoadout() {
  const loadout = logsLoadouts.value[Number(selectedLogsLoadout.value)]
  if (!loadout) return
  entries.value = loadout.entries.map(snapshot)
  sharedLoadout.value = loadout.loadout || null
  importWarnings.value = loadout.warnings || []
  importedVersion.value = sharedLoadout.value ? `GBFR Logs / gbfr-loadout v${sharedLoadout.value.version}` : 'GBFR Logs'
  importedComment.value = [loadout.playerName, loadout.characterName].filter(Boolean).join(' / ')
  writeIndex.value = 0
  show(t('sigil.loadout.selected', { player: importedComment.value || t('sigil.loadout.player'), count: entries.value.length }), 'success')
}

async function importLogs() {
  if (mode.value !== 'idle') return
  try {
    logsRecords.value = await SelectLogsSigilLoadouts() || []
    if (!logsRecords.value.length) throw new Error(t('sigil.loadout.logsEmpty'))
    selectedLogsRecord.value = '0'
    logsDialogOpen.value = true
  } catch (error) { show(t('sigil.loadout.logsFailed', { error: String(error) }), 'error') }
}

function confirmLogsRecord() {
  selectLogsRecord()
  logsDialogOpen.value = false
  show(t('sigil.loadout.logsRead', { index: Number(selectedLogsRecord.value) + 1, count: logsLoadouts.value.length }), 'success')
}

function closeLogsDialog() { logsDialogOpen.value = false }

function exportJSON() {
  if (!entries.value.length) { show(t('sigil.loadout.nothingExport'), 'error'); return }
  const payload = sharedLoadout.value
    ? sharedLoadout.value
    : { format: FORMAT, version: VERSION, loadoutVersion: exportVersion.value.trim(), comment: exportComment.value.trim(), entries: entries.value }
  const data = JSON.stringify(payload, null, 2)
  const url = URL.createObjectURL(new Blob([data], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = sharedLoadout.value ? 'gbfr-loadout.json' : 'gbfr-sigil-loadout.json'
  link.click()
  URL.revokeObjectURL(url)
}

function chooseImport() { fileInput.value?.click() }
async function importJSON(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file || mode.value !== 'idle') return
  try {
    const data = JSON.parse(await file.text())
    const complete = validCompleteLoadout(data)
    const sourceEntries = complete
      ? data.sigils.map(item => ({ sigilHash: parseInt(item.hash, 16), sigilLevel: item.level, primaryTraitHash: parseInt(item.primaryTraitHash, 16), primaryTraitLevel: item.primaryTraitLevel, secondaryTraitHash: parseInt(item.secondaryTraitHash || '0', 16), secondaryTraitLevel: item.secondaryTraitLevel || 0 }))
      : data?.entries
    if ((!complete && (data?.format !== FORMAT || data?.version !== VERSION)) || !Array.isArray(sourceEntries)) {
      throw new Error(t('sigil.loadout.invalidFile'))
    }
    if (!sourceEntries.length || sourceEntries.length > MAX_ENTRIES || !sourceEntries.every(validEntry)) {
      throw new Error(t('sigil.loadout.invalidEntries'))
    }
    entries.value = sourceEntries.map(snapshot)
    sharedLoadout.value = complete ? data : null
    importWarnings.value = []
    exportVersion.value = complete ? `gbfr-loadout v${data.version}` : (typeof data.loadoutVersion === 'string' ? data.loadoutVersion : '')
    exportComment.value = complete ? (typeof data.name === 'string' ? data.name : '') : (typeof data.comment === 'string' ? data.comment : '')
    importedVersion.value = exportVersion.value
    importedComment.value = exportComment.value
    writeIndex.value = 0
    show(t('sigil.loadout.imported', { count: entries.value.length }), 'success')
  } catch (error) { show(t('sigil.loadout.importFailed', { error: String(error) }), 'error') }
}

onMounted(async () => {
  try {
    const result = await SigilMemoryGetOptions()
    options.sigils = result.sigils || []
    options.traits = result.traits || []
  } catch (error) { show(t('sigil.memory.optionsFailed', { error: String(error) }), 'error') }
})
onBeforeUnmount(stop)
</script>

<template>
  <div class="loadout">
    <div v-if="logsDialogOpen" class="logs-dialog-mask" @click.self="closeLogsDialog">
      <section class="logs-dialog" role="dialog" aria-modal="true" aria-labelledby="logs-dialog-title">
        <div id="logs-dialog-title" class="section-title">{{ t('sigil.loadout.logsTitle') }} <span>{{ t('sigil.loadout.available', { count: logsRecords.length }) }}</span></div>
        <div class="logs-records">
          <label v-for="(record, index) in logsRecords" :key="index" class="logs-record" :class="{ selected: selectedLogsRecord === String(index) }">
            <input v-model="selectedLogsRecord" type="radio" name="logs-record" :value="String(index)" />
            <span>{{ new Date(record.logTime).toLocaleString() }}</span>
            <small>{{ t('sigil.loadout.players', { count: record.loadouts.length }) }}</small>
          </label>
        </div>
        <div class="actions">
          <button class="btn" @click="closeLogsDialog">{{ t('sigil.common.cancel') }}</button>
          <button class="btn btn-logs" @click="confirmLogsRecord">{{ t('sigil.loadout.confirmImport') }}</button>
        </div>
      </section>
    </div>
    <section class="section">
      <div class="section-title">{{ t('sigil.loadout.title') }} <span>{{ modeText }}</span></div>
      <div class="actions">
        <button class="btn btn-record" :disabled="mode !== 'idle'" @click="startRecord">{{ t('sigil.loadout.startRecording') }}</button>
        <button class="btn btn-write" :disabled="mode !== 'idle' || !entries.length" @click="startWrite">{{ t('sigil.loadout.startWriting') }}</button>
        <button class="btn" :disabled="mode === 'idle'" @click="stop">{{ t('sigil.loadout.stop') }}</button>
        <button class="btn" :disabled="mode !== 'idle' || !entries.length" @click="exportJSON">{{ t('sigil.loadout.export') }}</button>
        <button class="btn" :disabled="mode !== 'idle'" @click="chooseImport">{{ t('sigil.loadout.import') }}</button>
        <button class="btn btn-logs" :disabled="mode !== 'idle'" @click="importLogs">{{ t('sigil.loadout.importLogs') }}</button>
        <button class="btn btn-danger" :disabled="mode !== 'idle' || !entries.length" @click="clearEntries">{{ t('sigil.common.clear') }}</button>
        <input ref="fileInput" class="file-input" type="file" accept="application/json,.json" @change="importJSON" />
      </div>
      <div v-if="logsLoadouts.length" class="logs-picker">
        <label>
          <span>{{ t('sigil.loadout.logsTeamPlayer') }}</span>
          <select v-model="selectedLogsLoadout" :disabled="mode !== 'idle'" @change="selectLogsLoadout">
            <option v-for="(loadout, index) in logsLoadouts" :key="index" :value="String(index)">
              {{ loadout.playerName || t('sigil.loadout.unnamedPlayer') }}<template v-if="loadout.characterName">{{ t('sigil.loadout.playerCharacter', { name: loadout.characterName }) }}</template>{{ t('sigil.loadout.availableWrap', { count: loadout.entries.length }) }}
            </option>
          </select>
        </label>
      </div>
      <div class="export-fields">
        <label>
          <span>{{ t('sigil.loadout.version') }}</span>
          <input v-model="exportVersion" :disabled="mode !== 'idle'" maxlength="80" :placeholder="t('sigil.loadout.versionPlaceholder')" />
        </label>
        <label>
          <span>{{ t('sigil.loadout.comment') }}</span>
          <textarea v-model="exportComment" :disabled="mode !== 'idle'" maxlength="500" rows="2" :placeholder="t('sigil.loadout.commentPlaceholder')" />
        </label>
      </div>
      <div v-if="importedVersion || importedComment" class="imported-meta">
        <span v-if="importedVersion">{{ t('sigil.loadout.importVersion', { version: importedVersion }) }}</span>
        <span v-if="importedComment">{{ t('sigil.loadout.importComment', { comment: importedComment }) }}</span>
      </div>
      <div v-if="importWarnings.length" class="import-warnings">
        <strong>{{ t('sigil.loadout.importWarnings') }}</strong>
        <span v-for="(warning, index) in importWarnings" :key="index">{{ warning }}</span>
      </div>
      <p class="hint">{{ t('sigil.loadout.hint') }}</p>
    </section>

    <section class="section">
      <div class="section-title">{{ t('sigil.loadout.contents') }} <span>{{ entries.length }}/{{ MAX_ENTRIES }}</span></div>
      <div v-if="!entries.length" class="empty">{{ t('sigil.loadout.empty') }}</div>
      <ol v-else class="entries">
        <li v-for="(entry, index) in entries" :key="index" :class="{ active: mode === 'write' && index === writeIndex }">
          <span class="entry-index">{{ index + 1 }}</span>
          <span class="entry-detail">{{ detail(entry) }}</span>
        </li>
      </ol>
    </section>
  </div>
</template>

<style scoped>
.loadout { width:100%; display:flex; flex-direction:column; gap:14px; }
.logs-dialog-mask { position:fixed; z-index:1000; inset:0; display:flex; align-items:center; justify-content:center; padding:20px; background:rgba(0,0,0,.62); }
.logs-dialog { box-sizing:border-box; width:min(560px, 100%); max-height:min(680px, 100%); overflow:auto; padding:16px; border:1px solid rgba(255,255,255,.14); border-radius:9px; background:#252525; box-shadow:0 18px 60px rgba(0,0,0,.55); }
.logs-records { display:flex; flex-direction:column; gap:6px; margin-top:12px; }
.logs-record { display:grid; grid-template-columns:auto 1fr auto; align-items:center; gap:9px; padding:10px; border:1px solid rgba(255,255,255,.1); border-radius:6px; color:rgba(255,255,255,.76); cursor:pointer; font-size:.78rem; }
.logs-record.selected { border-color:rgba(251,191,36,.55); background:rgba(251,191,36,.08); }
.logs-record small { color:rgba(255,255,255,.42); }
.section { padding:14px 16px; border:1px solid rgba(255,255,255,.08); border-radius:8px; background:rgba(255,255,255,.04); }
.section-title { display:flex; justify-content:space-between; gap:12px; color:rgba(255,255,255,.7); font-size:.78rem; font-weight:600; }
.section-title span { color:rgba(255,255,255,.35); font-weight:400; }
.actions { display:flex; flex-wrap:wrap; gap:8px; margin-top:12px; }
.btn { padding:7px 12px; border:1px solid rgba(255,255,255,.15); border-radius:6px; background:rgba(255,255,255,.05); color:rgba(255,255,255,.75); font:600 .75rem inherit; cursor:pointer; }
.btn:disabled { opacity:.35; cursor:not-allowed; }
.btn-record { border-color:rgba(103,232,249,.35); color:#67e8f9; background:rgba(103,232,249,.1); }
.btn-write { border-color:rgba(74,222,128,.35); color:#4ade80; background:rgba(74,222,128,.1); }
.btn-logs { border-color:rgba(251,191,36,.35); color:#fbbf24; background:rgba(251,191,36,.08); }
.btn-danger { border-color:rgba(248,113,113,.35); color:#f87171; background:rgba(248,113,113,.08); }
.file-input { display:none; }
.logs-picker { margin-top:12px; }
.logs-picker label { display:flex; flex-direction:column; gap:5px; color:rgba(255,255,255,.48); font-size:.7rem; }
.logs-picker select { box-sizing:border-box; width:100%; padding:7px 9px; border:1px solid rgba(255,255,255,.13); border-radius:6px; background:#2a2a2a; color:rgba(255,255,255,.85); font:inherit; font-size:.75rem; outline:none; }
.logs-picker select:disabled { opacity:.45; }
.export-fields { display:grid; grid-template-columns:minmax(140px, .45fr) 1fr; gap:10px; margin-top:12px; }
.export-fields label { display:flex; flex-direction:column; gap:5px; color:rgba(255,255,255,.48); font-size:.7rem; }
.export-fields input, .export-fields textarea { box-sizing:border-box; width:100%; padding:7px 9px; border:1px solid rgba(255,255,255,.13); border-radius:6px; background:rgba(255,255,255,.05); color:rgba(255,255,255,.85); font:inherit; font-size:.75rem; outline:none; resize:vertical; }
.export-fields input:focus, .export-fields textarea:focus { border-color:rgba(103,232,249,.4); }
.export-fields input:disabled, .export-fields textarea:disabled { opacity:.45; }
.imported-meta, .import-warnings { display:flex; flex-direction:column; gap:3px; margin-top:10px; font-size:.7rem; }
.imported-meta { color:rgba(103,232,249,.7); }
.import-warnings { color:rgba(251,191,36,.82); }
.hint, .empty { margin:10px 0 0; color:rgba(255,255,255,.4); font-size:.72rem; line-height:1.5; }
@media (max-width:560px) { .export-fields { grid-template-columns:1fr; } }
.entries { list-style:none; margin:10px 0 0; padding:0; display:flex; flex-direction:column; gap:3px; }
.entries li { display:flex; align-items:center; gap:10px; padding:8px 10px; border-radius:5px; background:rgba(255,255,255,.035); color:rgba(255,255,255,.75); font-size:.75rem; }
.entries li.active { border:1px solid rgba(74,222,128,.4); background:rgba(74,222,128,.08); }
.entry-index { width:20px; color:#67e8f9; font-family:ui-monospace,Consolas,monospace; text-align:right; flex-shrink:0; }
.entry-detail { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
</style>
