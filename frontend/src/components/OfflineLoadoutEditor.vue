<script setup>
import { computed, onMounted, ref } from 'vue'
import { LoadoutApply, LoadoutApplyWithResources, LoadoutCheckCompliance, LoadoutDetail, LoadoutEditContext, LoadoutExportJSON, LoadoutImportJSON, LoadoutList, LogsMasteryNodePool, MasteryNodePool, MasterySummarize } from '../../wailsjs/go/main/OfflineLoadoutService'
import { GetLastSavePath, ParseLogsSigilLoadoutsJSON, SetLastSavePath } from '../../wailsjs/go/main/App'
import { translate as t } from '../i18n'

const emit = defineEmits(['status'])
const savePath = ref('C:\\Users\\Username\\AppData\\Local\\GBFR\\Saved\\SaveGames\\SaveData1.dat')
const groups = ref([])
const selectedCharacter = ref('')
const context = ref(null)
const detail = ref(null)
const selectedSlot = ref('')
const busy = ref(false)
const result = ref(null)
const form = ref(emptyForm())
const importedDraft = ref(null)
const importedShare = ref(null)
const importFile = ref(null)
const logsJSON = ref('')
const logsRecords = ref([])
const selectedLogRecord = ref('')
const selectedLogPlayer = ref('')
const pendingLogImport = ref(null)
const logsMasteryPools = ref([])
const logsMasteryActive = ref([])
const masteryPools = ref([])
const masterySummary = ref(null)
const masteryExpanded = ref({ SB_DEF: true, SB_ATK: true, SB_LIMIT: true })

function emptyForm() {
  return { unitId: 0, expectCharaHash: '', op: 'write', name: '', weaponSlotId: 0, sigilSlotIds: [], summonSlotIds: [], skillHashes: [], weaponSkillHashes: [], masteryHashes: [] }
}
function show(message, type) { emit('status', message, type) }
function hex(value) { return `0x${(Number(value) >>> 0).toString(16).toUpperCase().padStart(8, '0')}` }
function text(value) { return value || t('offlineLoadout.empty') }
function formatSigil(item) {
  const secondary = item.secondaryTraitHash ? ` / ${text(item.secondaryTraitName || item.secondaryTraitHash)} ${t('offlineLoadout.level')} ${item.secondaryTraitLevel}` : ''
  return `${Number(item.index) + 1}. ${text(item.name || item.hash)} ${t('offlineLoadout.level')} ${item.level} · ${text(item.primaryTraitName || item.primaryTraitHash)} ${t('offlineLoadout.level')} ${item.primaryTraitLevel}${secondary}`
}
function formatSummon(item, index) {
  const main = text(item.mainTraitName || item.mainTraitHash)
  const sub = text(item.subParamName || item.subParamHash)
  return `${index + 1}. ${text(item.name || item.typeHash)} · ${t('offlineLoadout.mainBlessing')} ${main} ${t('offlineLoadout.level')} ${item.mainTraitLevel} / ${t('offlineLoadout.subParameter')} ${sub} ${t('offlineLoadout.level')} ${item.subParamLevel} · ${t('offlineLoadout.rank')} ${item.rank}`
}
function enhancementText() {
  const value = detail.value?.character
  if (!value) return t('offlineLoadout.enhancement.unavailable')
  const panel = (value.enhancementPanel || []).join('、') || t('offlineLoadout.empty')
  const nodes = value.enhancementNodes || []
  return t('offlineLoadout.enhancement.panel', { panel, nodes: nodes.length ? nodes.map(item => `${item.index}:${item.value}`).join('、') : t('offlineLoadout.empty') })
}
function exportCurrent() {
  if (!selected.value) return show(t('offlineLoadout.error.selectSlot'), 'error')
  LoadoutExportJSON(savePath.value.trim(), Number(selected.value.unitId)).then(payload => {
    const url = URL.createObjectURL(new Blob([payload], { type: 'application/json' }))
    const link = document.createElement('a'); link.href = url; link.download = 'gbfr-loadout.json'; link.click(); URL.revokeObjectURL(url)
    show(t('offlineLoadout.status.exported'), 'success')
  }).catch(error => show(t('offlineLoadout.status.exportFailed', { error: String(error) }), 'error'))
}
async function importPayload(payload, logsEffectUIIds = []) {
  if (!context.value) return show(t('offlineLoadout.error.selectCharacterSlot'), 'error')
  busy.value = true
  try {
    const share = JSON.parse(payload)
    const draft = await LoadoutImportJSON(savePath.value.trim(), context.value.charaHash, payload)
    importedDraft.value = draft
    importedShare.value = { ...share, masteryHashes: draft.masteryHashes || [] }
    const sourceLogsEffectUIIds = logsEffectUIIds.length ? logsEffectUIIds : share.logsSkillboardEffectUiIds || []
    logsMasteryActive.value = Array.from(new Set(sourceLogsEffectUIIds.map(Number).filter(Number.isFinite)))
    logsMasteryPools.value = logsMasteryActive.value.length ? await LogsMasteryNodePool(context.value.ownerCode, logsMasteryActive.value) || [] : []
    const [pools, summary] = await Promise.all([
      MasteryNodePool(context.value.ownerCode),
      MasterySummarize(context.value.ownerCode, draft.masteryHashes || []),
    ])
    masteryPools.value = pools || []
    masterySummary.value = summary
    form.value = { ...emptyForm(), unitId: Number(selectedSlot.value), expectCharaHash: context.value.charaHash, op: 'write', name: draft.name || form.value.name, weaponSlotId: Number(draft.weaponSlotId || 0), sigilSlotIds: (draft.sigilSlotIds || []).map(Number), summonSlotIds: (draft.summonSlotIds || []).map(Number), skillHashes: draft.skillHashes || [], weaponSkillHashes: draft.weaponSkillHashes || [], masteryHashes: draft.masteryHashes || [] }
    show(t('offlineLoadout.status.importedDraft', { name: draft.name || t('offlineLoadout.unnamedLoadout') }), 'success')
  } catch (error) { show(t('offlineLoadout.error.importFailed', { error: String(error) }), 'error') } finally { busy.value = false }
}
async function importFileJSON(event) {
  const file = event.target.files?.[0]; event.target.value = ''
  if (!file) return
  try { await importPayload(await file.text()) } catch (error) { show(t('offlineLoadout.error.readFileFailed', { error: String(error) }), 'error') }
}
async function loadLogs() {
  if (!logsJSON.value.trim()) return show(t('offlineLoadout.error.pasteLogs'), 'error')
  try {
    logsRecords.value = await ParseLogsSigilLoadoutsJSON(logsJSON.value) || []
    selectedLogRecord.value = '0'
    selectedLogPlayer.value = '0'
    show(t('offlineLoadout.status.logsParsed', { count: logsRecords.value.length }), 'success')
  } catch (error) { show(t('offlineLoadout.error.parseLogsFailed', { error: String(error) }), 'error') }
}
function logRecordLabel(record) {
  if (!Number(record.logTime)) return record.questName || t('offlineLoadout.logsExport')
  const quest = record.questName && !String(record.questName).startsWith(t('offlineLoadout.unknownQuest')) ? ` · ${record.questName}` : ''
  return `${new Date(record.logTime).toLocaleString()}${quest} (${t('offlineLoadout.players', { count: record.loadouts.length })})`
}
function logPlayerCharacter(player) { return player?.characterName || player?.characterType || player?.loadout?.ownerCode || t('offlineLoadout.unknownCharacter') }
function normalizeOwnerCode(value) { return String(value || '').trim().replace(/\0/g, '').toUpperCase() }
function clearImportedDraft() {
  importedDraft.value = null
  importedShare.value = null
}
async function switchToLogPlayerCharacter(player) {
  const ownerCode = normalizeOwnerCode(player.loadout?.ownerCode || player.characterType)
  const name = String(player.loadout?.charaName || player.characterName || '').trim()
  const preferred = groups.value.find(group => name && group.charaName === name)
  const candidates = preferred ? [preferred, ...groups.value.filter(group => group !== preferred)] : groups.value
  for (const group of candidates) {
    const candidateContext = await LoadoutEditContext(savePath.value.trim(), group.charaHash)
    if (ownerCode && normalizeOwnerCode(candidateContext?.ownerCode) !== ownerCode) continue
    selectedCharacter.value = group.charaHash
    context.value = candidateContext
    selectedSlot.value = ''
    clearImportedDraft()
    form.value = emptyForm()
    result.value = null
    detail.value = null
    masteryPools.value = []
    masterySummary.value = null
    logsMasteryPools.value = []
    logsMasteryActive.value = []
    return true
  }
  return false
}
async function importLogPlayer() {
  const player = logsRecords.value[Number(selectedLogRecord.value)]?.loadouts?.[Number(selectedLogPlayer.value)]
  if (!player?.loadout) return show(t('offlineLoadout.error.noLogLoadout'), 'error')
  busy.value = true
  try {
    if (!await switchToLogPlayerCharacter(player)) throw new Error(t('offlineLoadout.error.logCharacterNotFound', { owner: player.loadout?.ownerCode || player.characterType || t('offlineLoadout.unknownCharacter') }))
    for (const warning of player.warnings || []) show(warning, 'error')
    pendingLogImport.value = player.loadout
    logsMasteryPools.value = []
    logsMasteryActive.value = []
    show(t('offlineLoadout.status.logCharacterSelected'), 'success')
  } catch (error) { show(t('offlineLoadout.error.importFailed', { error: String(error) }), 'error') } finally { busy.value = false }
}
async function confirmLogImport() {
  if (!pendingLogImport.value) return
  if (!selected.value) return show(t('offlineLoadout.error.selectTargetSlot'), 'error')
  await importPayload(JSON.stringify(pendingLogImport.value), pendingLogImport.value.logsSkillboardEffectUiIds || [])
  pendingLogImport.value = null
}
async function loadMasteryLayout() {
  if (!context.value || !detail.value) return
  try {
    const [pools, summary] = await Promise.all([
      MasteryNodePool(context.value.ownerCode),
      MasterySummarize(context.value.ownerCode, detail.value.masteryHashes || []),
    ])
    masteryPools.value = pools || []
    masterySummary.value = summary
  } catch (error) {
    masteryPools.value = []
    masterySummary.value = null
    show(t('offlineLoadout.error.readMasteryFailed', { error: String(error) }), 'error')
  }
}
function masteryNodeActive(hash) { return displayMastery.value.includes(hash) }
function masteryCategoryActive(rank, cat) { return masterySummary.value?.ranks?.find(item => item.rank === rank)?.categories?.find(item => item.cat === cat)?.active || false }
function masteryNodes(rank, cat) {
  return rank.nodes.filter(item => item.cat === cat)
}
function masteryNodeDescription(node) {
  return node.hash === '1F52146F' ? t('offlineLoadout.mastery.stun') : node.desc || node.name || t('offlineLoadout.mastery.unknownEffect')
}
function toggleMasteryCategory(cat) {
  masteryExpanded.value[cat] = !masteryExpanded.value[cat]
}
function masterySpecialization(cat) {
  return masterySummary.value?.ranks?.[0]?.categories?.find(item => item.cat === cat)?.specialization || ''
}
const masteryCategories = [
  { cat: 'SB_DEF', label: t('offlineLoadout.mastery.awakening') },
  { cat: 'SB_ATK', label: t('offlineLoadout.mastery.truth') },
  { cat: 'SB_LIMIT', label: t('offlineLoadout.mastery.secret') },
]

async function copyMasteryEffects() {
  if (!masteryPools.value.length) return show(t('offlineLoadout.error.readSlot'), 'error')
  const lines = []
  for (const category of masteryCategories) {
    lines.push(`${category.label}: ${masterySpecialization(category.cat) || t('offlineLoadout.empty')}`)
    for (const rank of masteryPools.value) {
      const nodes = masteryNodes(rank, category.cat)
      lines.push(rank.rank === 'EX' ? t('offlineLoadout.exStage') : t('offlineLoadout.stage', { rank: rank.rank.slice(1) }))
      for (let index = 0; index < nodes.length; index += 2) {
        const cell = node => `${masteryNodeActive(node.hash) ? '■' : '□'} ${node.hash} | ${masteryNodeDescription(node)}`
        const left = cell(nodes[index])
        const right = nodes[index + 1] ? cell(nodes[index + 1]) : ''
        lines.push(`${left}\t|\t${right}`)
      }
    }
    lines.push('')
  }
  try {
    await navigator.clipboard.writeText(lines.join('\n'))
    show(t('offlineLoadout.status.copiedMastery'), 'success')
  } catch (error) { show(t('offlineLoadout.error.copyFailed', { error: String(error) }), 'error') }
}

const slots = computed(() => context.value?.slots || [])
const weapons = computed(() => context.value?.weapons || [])
const skills = computed(() => context.value?.skills || [])
const selected = computed(() => slots.value.find(item => Number(item.unitId) === Number(selectedSlot.value)))
const displayedDetail = computed(() => importedShare.value || detail.value)
const detailSigils = computed(() => detail.value?.sigils || [])
const detailSkills = computed(() => detail.value?.skills || [])
const detailSummons = computed(() => detail.value?.summons || [])
const detailMastery = computed(() => detail.value?.masteryHashes || [])
const detailOverLimit = computed(() => detail.value?.overLimit || [])
const displaySigils = computed(() => importedShare.value?.sigils || detailSigils.value)
const displaySkills = computed(() => importedShare.value?.skills || detailSkills.value)
const displaySummons = computed(() => importedShare.value?.summons || detailSummons.value)
const displayMastery = computed(() => importedShare.value?.masteryHashes || detailMastery.value)
const displayOverLimit = computed(() => importedShare.value?.overLimit || detailOverLimit.value)
const displayWeaponSkills = computed(() => {
  const weapon = displayedDetail.value?.weapon
  if (!weapon) return []
  if (weapon.skills?.length) return weapon.skills
  return (weapon.skillHashes || []).map((hash, index) => ({ slot: index, traitHash: hash, name: weapon.skillNames?.[index] || hash, level: weapon.skillLevels?.[index] ?? '—' }))
})
function formatOverLimit(item) {
  if (!item?.attributeHash) return t('offlineLoadout.emptySlot')
  const value = Number(item.value || 0)
  const suffix = item.unit === 'pct' ? '%' : ''
  return `${text(item.name || item.attributeHash)} ${t('offlineLoadout.level')} ${item.level}: +${value}${suffix}`
}

async function load() {
  if (!savePath.value.trim()) return show(t('offlineLoadout.error.savePath'), 'error')
  busy.value = true
  try {
    const path = savePath.value.trim()
    groups.value = await LoadoutList(path) || []
    if (!groups.value.length) throw new Error(t('offlineLoadout.error.noLoadouts'))
    await SetLastSavePath(path)
    selectedCharacter.value = groups.value[0].charaHash
    importedDraft.value = null
    importedShare.value = null
    await loadContext()
    show(t('offlineLoadout.status.loaded', { count: groups.value.length }), 'success')
  } catch (error) { show(t('offlineLoadout.error.loadFailed', { error: String(error) }), 'error') } finally { busy.value = false }
}
async function loadContext() {
  if (!selectedCharacter.value) return
  clearImportedDraft()
  busy.value = true
  try {
    context.value = await LoadoutEditContext(savePath.value.trim(), selectedCharacter.value)
    selectedSlot.value = String(context.value?.slots?.[0]?.unitId || '')
    await selectSlot()
  } catch (error) { show(t('offlineLoadout.error.readResourcesFailed', { error: String(error) }), 'error') } finally { busy.value = false }
}
async function selectSlot() {
  const item = selected.value
  if (!item) return
  if (importedDraft.value) {
    form.value = { ...form.value, unitId: Number(item.unitId), expectCharaHash: context.value.charaHash, op: 'write' }
  } else {
    form.value = {
      unitId: Number(item.unitId), expectCharaHash: context.value.charaHash, op: 'write', name: item.name || '',
      weaponSlotId: Number(item.weaponSlotId || 0), sigilSlotIds: (item.sigilSlotIds || []).filter(Boolean).map(Number), summonSlotIds: (item.summonSlotIds || []).filter(Boolean).map(Number),
      skillHashes: (item.skillHashes || []).filter(Boolean), weaponSkillHashes: (item.weaponSkillHashes || []).filter(Boolean), masteryHashes: (item.masteryHashes || []).filter(Boolean),
    }
  }
  result.value = null
  detail.value = null
  masteryPools.value = []
  masterySummary.value = null
  logsMasteryPools.value = []
  logsMasteryActive.value = []
  if (!savePath.value.trim()) return
  try {
    const value = await LoadoutDetail(savePath.value.trim(), Number(item.unitId))
    detail.value = value
    await loadMasteryLayout()
    for (const warning of value.warnings || []) show(warning, 'error')
  } catch (error) { show(t('offlineLoadout.error.readDetailFailed', { error: String(error) }), 'error') }
}
function toggleSigil(slotId) {
  slotId = Number(slotId)
  const values = form.value.sigilSlotIds
  const index = values.indexOf(slotId)
  if (index >= 0) values.splice(index, 1)
  else if (values.length < 12) values.push(slotId)
  else show(t('offlineLoadout.error.maxSigils'), 'error')
}
function toggleSkill(hash) {
  const values = form.value.skillHashes
  const index = values.indexOf(hash)
  if (index >= 0) values.splice(index, 1)
  else if (values.length < 4) values.push(hash)
  else show(t('offlineLoadout.error.maxSkills'), 'error')
}
async function preflight() {
  busy.value = true
  try {
    const report = await LoadoutCheckCompliance(savePath.value.trim(), form.value)
    result.value = { kind: t('offlineLoadout.preflight'), report }
    show(report.writable ? t('offlineLoadout.status.preflightPassed') : t('offlineLoadout.status.preflightRejected', { message: report.message }), report.writable ? 'success' : 'error')
  } catch (error) { show(t('offlineLoadout.error.preflightFailed', { error: String(error) }), 'error') } finally { busy.value = false }
}
async function apply(copy) {
  if (!confirm(copy ? t('offlineLoadout.confirm.copy') : t('offlineLoadout.confirm.overwrite'))) return
  busy.value = true
  try {
    const output = copy ? `${savePath.value.trim()}.loadout.dat` : ''
    const request = importedDraft.value ? { changes: [{ ...form.value, constructedSigils: importedDraft.value.constructedSigils || [] }], importPayload: importedDraft.value.applyPayload || null } : null
    const written = request ? await LoadoutApplyWithResources(savePath.value.trim(), output, request) : await LoadoutApply(savePath.value.trim(), output, [form.value])
    importedDraft.value = null
    importedShare.value = null
    logsMasteryPools.value = []
    logsMasteryActive.value = []
    result.value = { kind: t('offlineLoadout.writeCompleted'), report: written }
    show(t('offlineLoadout.status.written', { path: written.outputPath }), 'success')
    await loadContext()
  } catch (error) { show(t('offlineLoadout.error.writeFailed', { error: String(error) }), 'error') } finally { busy.value = false }
}
onMounted(async () => {
  try {
    const lastPath = await GetLastSavePath()
    if (lastPath?.trim()) savePath.value = lastPath.trim()
  } catch (error) {
    console.warn(t('offlineLoadout.error.readLastSavePath'), error)
  }
})
</script>

<template>
  <div class="loadout-editor">
    <section class="section">
      <div class="section-title">{{ t('offlineLoadout.ui.title') }} <span>{{ t('offlineLoadout.ui.subtitle') }}</span></div>
      <label class="field"><span>{{ t('offlineLoadout.ui.savePath') }}</span><input v-model="savePath" :placeholder="t('offlineLoadout.ui.savePathPlaceholder')" @keyup.enter="load" /></label>
      <div class="actions"><button class="btn primary" :disabled="busy" @click="load">{{ t('offlineLoadout.ui.load') }}</button><button class="btn" :disabled="busy || !context" @click="exportCurrent">{{ t('offlineLoadout.ui.export') }}</button><button class="btn" :disabled="busy || !context" @click="importFile?.click()">{{ t('offlineLoadout.ui.importJson') }}</button><input ref="importFile" class="file-input" type="file" accept="application/json,.json" @change="importFileJSON" /></div>
      <label class="field"><span>{{ t('offlineLoadout.ui.logsJson') }}</span><textarea v-model="logsJSON" rows="6" :placeholder="t('offlineLoadout.ui.logsPlaceholder')" /></label>
      <div class="actions"><button class="btn" :disabled="busy" @click="loadLogs">{{ t('offlineLoadout.ui.parseLogs') }}</button></div>
      <div v-if="logsRecords.length" class="logs-import"><label class="field"><span>{{ t('offlineLoadout.ui.logSession') }}</span><select v-model="selectedLogRecord"><option v-for="(record,index) in logsRecords" :key="index" :value="String(index)">{{ logRecordLabel(record) }}</option></select></label><label class="field"><span>{{ t('offlineLoadout.ui.player') }}</span><select v-model="selectedLogPlayer"><option v-for="(player,index) in (logsRecords[Number(selectedLogRecord)]?.loadouts || [])" :key="index" :value="String(index)">{{ player.playerName || t('offlineLoadout.ui.unnamedPlayer') }} / {{ logPlayerCharacter(player) }}</option></select></label><button class="btn" :disabled="busy" @click="importLogPlayer">{{ t('offlineLoadout.ui.importPlayer') }}</button></div>
      <p class="hint">{{ t('offlineLoadout.ui.writeHint') }}</p>
    </section>
    <section v-if="groups.length" class="section">
      <div class="section-title">{{ t('offlineLoadout.ui.characterTarget') }}</div>
      <label class="field"><span>{{ t('offlineLoadout.ui.character') }}</span><select v-model="selectedCharacter" :disabled="busy" @change="loadContext"><option v-for="item in groups" :key="item.charaHash" :value="item.charaHash">{{ text(item.charaName) }} / {{ item.charaHash }} ({{ t('offlineLoadout.ui.savedLoadouts', { count: item.loadouts.length }) }})</option></select></label>
      <label class="field"><span>{{ t('offlineLoadout.ui.targetSlot') }}</span><select v-model="selectedSlot" :disabled="busy" @change="selectSlot"><option disabled value="">{{ t('offlineLoadout.ui.selectTargetSlot') }}</option><option v-for="item in slots" :key="item.unitId" :value="String(item.unitId)">{{ item.name || t('offlineLoadout.emptySlot') }} · {{ t('offlineLoadout.ui.unitId') }} {{ item.unitId }}</option></select></label>
      <div v-if="pendingLogImport" class="actions"><button class="btn primary" :disabled="busy" @click="confirmLogImport">{{ t('offlineLoadout.ui.importTarget') }}</button></div>
      <div v-if="displayedDetail" class="summary"><div class="section-title">{{ t('offlineLoadout.ui.summary') }} <span>{{ importedShare ? t('offlineLoadout.ui.pendingDraft') : t('offlineLoadout.ui.readOnly') }}</span></div><p><b>{{ t('offlineLoadout.ui.character') }}：</b>{{ text(displayedDetail.charaName || selected?.charaName) }} ({{ text(displayedDetail.charaHash || selectedCharacter) }})</p><div><b>{{ t('offlineLoadout.ui.weapon') }}</b>{{ text(displayedDetail.weaponName || displayedDetail.weaponHash) }}</div><div v-if="displayedDetail.weapon"><b>{{ t('offlineLoadout.ui.weaponEnhancement') }}</b>{{ t('offlineLoadout.level') }} {{ displayedDetail.weapon.level || displayedDetail.weapon.xp || t('offlineLoadout.empty') }} / {{ t('offlineLoadout.ui.uncap') }} {{ displayedDetail.weapon.uncap }} / {{ t('offlineLoadout.ui.mirage') }} {{ displayedDetail.weapon.mirage }} / {{ t('offlineLoadout.ui.awakening') }} {{ displayedDetail.weapon.awakening }} / {{ t('offlineLoadout.ui.transcendence') }} {{ displayedDetail.weapon.transcendence }}</div><div class="equipment-skills-summary"><div><b>{{ t('offlineLoadout.ui.characterOverlimit') }}</b><ol v-if="displayOverLimit.length"><li v-for="item in displayOverLimit" :key="item.index">{{ formatOverLimit(item) }}</li></ol><span v-else>{{ t('offlineLoadout.empty') }}</span></div><div><b>{{ t('offlineLoadout.ui.weaponSkills') }}</b><ol v-if="displayWeaponSkills.length"><li v-for="item in displayWeaponSkills" :key="`${item.slot}-${item.traitHash}`">{{ item.slot + 1 }}. {{ text(item.name || item.traitHash) }} {{ t('offlineLoadout.level') }} {{ item.level }}</li></ol><span v-else>{{ t('offlineLoadout.empty') }}</span></div><div><b>{{ t('offlineLoadout.ui.weaponBlessing') }}</b><template v-if="displayedDetail.weapon?.wrightstone">{{ text(displayedDetail.weapon.wrightstone.name || displayedDetail.weapon.wrightstone.hash) }}<ol v-if="displayedDetail.weapon.wrightstone.traits?.length"><li v-for="item in displayedDetail.weapon.wrightstone.traits" :key="`${item.index}-${item.hash}`">{{ item.index + 1 }}. {{ text(item.name || item.hash) }} {{ t('offlineLoadout.level') }} {{ item.level }}</li></ol></template><span v-else>{{ t('offlineLoadout.empty') }}</span></div><ul v-if="displaySkills.length"><li v-for="item in displaySkills" :key="item.hash">{{ text(item.name || item.hash) }} ({{ item.hash }})</li></ul><span v-else>{{ t('offlineLoadout.empty') }}</span></div><div><b>{{ t('offlineLoadout.ui.summons') }}</b><ol v-if="displaySummons.length"><li v-for="(item,index) in displaySummons" :key="`${index}-${item.typeHash}`">{{ formatSummon(item, index) }}</li></ol><span v-else>{{ t('offlineLoadout.empty') }}</span></div><div><b>{{ t('offlineLoadout.ui.sigils') }}</b><ol v-if="displaySigils.length"><li v-for="item in displaySigils" :key="`${item.index}-${item.slotId || item.hash}`">{{ formatSigil(item) }}</li></ol><span v-else>{{ t('offlineLoadout.empty') }}</span></div><div class="mastery-layout" v-if="masteryPools.length"><div class="mastery-tools"><b>{{ t('offlineLoadout.ui.masteryMap') }}</b><button class="btn copy-mastery" @click="copyMasteryEffects">{{ t('offlineLoadout.ui.copyMastery') }}</button></div><div v-for="category in masteryCategories" :key="category.cat" class="mastery-category"><button class="mastery-heading" type="button" @click="toggleMasteryCategory(category.cat)"><span>{{ masteryExpanded[category.cat] ? '▼' : '▶' }} {{ category.label }}</span></button><template v-if="masteryExpanded[category.cat]"><div v-for="rank in masteryPools" :key="rank.rank" class="mastery-rank"><span class="mastery-rank-label">{{ rank.rank === 'EX' ? t('offlineLoadout.exStage') : t('offlineLoadout.stage', { rank: rank.rank.slice(1) }) }}</span><span class="mastery-node-list"><span v-for="node in masteryNodes(rank, category.cat)" :key="node.hash" class="mastery-node" :class="{ active: masteryNodeActive(node.hash) }"><b>{{ masteryNodeActive(node.hash) ? '■' : '□' }}</b><span>{{ masteryNodeDescription(node) }}</span></span></span></div></template></div><small>{{ t('offlineLoadout.ui.masteryHint') }}</small></div><p><b>{{ t('offlineLoadout.ui.characterEnhancement') }}</b>{{ enhancementText() }}</p></div>
      <label class="field"><span>{{ t('offlineLoadout.ui.loadoutName') }}</span><input v-model="form.name" maxlength="63" /></label>
    </section>
    <section v-if="context" class="section"><div class="section-title">{{ t('offlineLoadout.ui.importTitle') }} <span>{{ importedDraft ? t('offlineLoadout.ui.draftLoaded') : t('offlineLoadout.ui.importPrompt') }}</span></div><p class="hint">{{ t('offlineLoadout.ui.importHint') }}</p><div class="actions"><button class="btn" :disabled="busy || !importedDraft" @click="preflight">{{ t('offlineLoadout.preflight') }}</button><button class="btn" :disabled="busy || !importedDraft" @click="apply(true)">{{ t('offlineLoadout.ui.writeCopy') }}</button><button class="btn danger" :disabled="busy || !importedDraft" @click="apply(false)">{{ t('offlineLoadout.ui.overwrite') }}</button></div></section>
    <section v-if="result" class="section result"><div class="section-title">{{ result.kind }}</div><pre>{{ JSON.stringify(result.report, null, 2) }}</pre></section>
  </div>
</template>

<style scoped>
.loadout-editor{display:flex;flex-direction:column;gap:14px}.section{padding:14px 16px;border:1px solid rgba(255,255,255,.08);border-radius:8px;background:rgba(255,255,255,.04)}.equipment-skills-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-top:8px}.mastery-layout{margin-top:10px}.mastery-tools{display:flex;align-items:center;justify-content:space-between;gap:10px}.copy-mastery{padding:4px 8px;font-size:.68rem}.mastery-category{margin:10px 0;padding:9px 10px;border-left:2px solid rgba(255,255,255,.14);background:rgba(255,255,255,.025)}.mastery-heading{display:block;width:100%;margin-bottom:6px;padding:0;border:0;background:transparent;color:rgba(255,255,255,.84);font:inherit;font-weight:600;text-align:left;cursor:pointer}.mastery-heading:hover{color:#67e8f9}.mastery-rank{display:flex;align-items:flex-start;gap:8px;min-height:25px}.mastery-rank-label{width:42px;flex:0 0 42px;padding-top:1px;color:rgba(255,255,255,.42);font-size:.7rem}.mastery-rank-label.active{color:#67e8f9}.mastery-node-list{display:flex;min-width:0;flex:1;flex-direction:column;gap:4px}.mastery-node{display:flex;gap:6px;color:rgba(255,255,255,.48);font-size:.72rem;line-height:1.4}.mastery-node b{flex:0 0 auto;color:rgba(255,255,255,.2);font-size:.86rem;line-height:1}.mastery-node.active{color:rgba(255,255,255,.82)}.mastery-node.active b{color:#38bdf8;text-shadow:0 0 7px rgba(56,189,248,.75)}.mastery-empty{color:rgba(255,255,255,.28);font-size:.72rem}.mastery-layout small{display:block;margin-top:5px;color:rgba(255,255,255,.4);font-size:.68rem}.logs-import{margin-top:12px;padding:10px 12px;border:1px solid rgba(251,191,36,.25);border-radius:6px;background:rgba(251,191,36,.04)}.file-input{display:none}.summary{margin-top:12px;padding:12px;border:1px solid rgba(103,232,249,.22);border-radius:6px;background:rgba(103,232,249,.045);color:rgba(255,255,255,.7);font-size:.73rem;line-height:1.55}.draft-summary{border-color:rgba(251,191,36,.32);background:rgba(251,191,36,.045)}.summary p{margin:8px 0}.summary ol,.summary ul{margin:5px 0 8px;padding-left:22px}.summary li{margin:3px 0}.section-title{display:flex;justify-content:space-between;gap:8px;color:rgba(255,255,255,.78);font-size:.8rem;font-weight:600}.section-title span{color:rgba(255,255,255,.4);font-weight:400}.field{display:flex;flex-direction:column;gap:5px;margin-top:10px;color:rgba(255,255,255,.5);font-size:.72rem}.field input,.field select,.field textarea,.grid select{box-sizing:border-box;width:100%;padding:7px 9px;border:1px solid rgba(255,255,255,.13);border-radius:6px;background:#2a2a2a;color:rgba(255,255,255,.88);font:inherit;font-size:.75rem}.field textarea{resize:vertical;font-family:ui-monospace,Consolas,monospace}.actions{display:flex;flex-wrap:wrap;gap:8px;margin-top:12px}.btn{padding:7px 12px;border:1px solid rgba(255,255,255,.15);border-radius:6px;background:rgba(255,255,255,.05);color:rgba(255,255,255,.8);font:600 .75rem inherit;cursor:pointer}.btn:disabled{opacity:.4;cursor:not-allowed}.primary{border-color:rgba(103,232,249,.35);color:#67e8f9}.danger{border-color:rgba(248,113,113,.45);color:#f87171}.hint{margin:10px 0 0;color:rgba(255,255,255,.42);font-size:.72rem;line-height:1.5}.grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px}.check{display:block;margin-top:7px;color:rgba(255,255,255,.72);font-size:.72rem;word-break:break-all}.check input{margin-right:6px}.result pre{margin:10px 0 0;max-height:300px;overflow:auto;white-space:pre-wrap;word-break:break-word;color:rgba(255,255,255,.65);font-size:.7rem}@media(max-width:760px){.grid,.equipment-skills-summary{grid-template-columns:1fr}}
</style>
