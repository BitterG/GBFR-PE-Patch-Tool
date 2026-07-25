<script setup>
import { computed, ref } from 'vue'
import { LoadoutApply, LoadoutApplyWithResources, LoadoutCheckCompliance, LoadoutDetail, LoadoutEditContext, LoadoutExportJSON, LoadoutImportJSON, LoadoutList, MasteryNodePool, MasterySummarize } from '../../wailsjs/go/main/OfflineLoadoutService'
import { SelectLogsSigilLoadouts } from '../../wailsjs/go/main/App'

const emit = defineEmits(['status'])
const savePath = ref('')
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
const logsRecords = ref([])
const selectedLogRecord = ref('')
const selectedLogPlayer = ref('')
const pendingLogImport = ref(null)
const masteryPools = ref([])
const masterySummary = ref(null)
const masteryExpanded = ref({ SB_DEF: true, SB_ATK: true, SB_LIMIT: true })

function emptyForm() {
  return { unitId: 0, expectCharaHash: '', op: 'write', name: '', weaponSlotId: 0, sigilSlotIds: [], summonSlotIds: [], skillHashes: [], weaponSkillHashes: [], masteryHashes: [] }
}
function show(message, type) { emit('status', message, type) }
function hex(value) { return `0x${(Number(value) >>> 0).toString(16).toUpperCase().padStart(8, '0')}` }
function text(value) { return value || '—' }
function formatSigil(item) {
  const secondary = item.secondaryTraitHash ? ` / ${text(item.secondaryTraitName || item.secondaryTraitHash)} Lv ${item.secondaryTraitLevel}` : ''
  return `${Number(item.index) + 1}. ${text(item.name || item.hash)} Lv ${item.level} · ${text(item.primaryTraitName || item.primaryTraitHash)} Lv ${item.primaryTraitLevel}${secondary}`
}
function formatSummon(item, index) {
  const main = text(item.mainTraitName || item.mainTraitHash)
  const sub = text(item.subParamName || item.subParamHash)
  return `${index + 1}. ${text(item.name || item.typeHash)} · 主加护 ${main} Lv ${item.mainTraitLevel} / 副参数 ${sub} Lv ${item.subParamLevel} · Rank ${item.rank}`
}
function enhancementText() {
  const value = detail.value?.character
  if (!value) return '当前存档未提供角色强化数据'
  const panel = (value.enhancementPanel || []).join('、') || '—'
  const nodes = value.enhancementNodes || []
  return `强化面板：${panel}；强化节点：${nodes.length ? nodes.map(item => `${item.index}:${item.value}`).join('、') : '—'}`
}
function exportCurrent() {
  if (!selected.value) return show('请先选择配装槽位', 'error')
  LoadoutExportJSON(savePath.value.trim(), Number(selected.value.unitId)).then(payload => {
    const url = URL.createObjectURL(new Blob([payload], { type: 'application/json' }))
    const link = document.createElement('a'); link.href = url; link.download = 'gbfr-loadout.json'; link.click(); URL.revokeObjectURL(url)
    show('已导出当前槽位的完整配装', 'success')
  }).catch(error => show(`导出失败: ${String(error)}`, 'error'))
}
async function importPayload(payload) {
  if (!context.value) return show('请先读取目标角色与槽位', 'error')
  busy.value = true
  try {
    const share = JSON.parse(payload)
    const draft = await LoadoutImportJSON(savePath.value.trim(), context.value.charaHash, payload)
    importedDraft.value = draft
    importedShare.value = share
    masterySummary.value = await MasterySummarize(context.value.ownerCode, draft.masteryHashes || [])
    form.value = { ...emptyForm(), unitId: Number(selectedSlot.value), expectCharaHash: context.value.charaHash, op: 'write', name: draft.name || form.value.name, weaponSlotId: Number(draft.weaponSlotId || 0), sigilSlotIds: (draft.sigilSlotIds || []).map(Number), summonSlotIds: (draft.summonSlotIds || []).map(Number), skillHashes: draft.skillHashes || [], weaponSkillHashes: draft.weaponSkillHashes || [], masteryHashes: draft.masteryHashes || [] }
    show(`已导入草稿：${draft.name || '未命名配装'}；请选择目标槽位后预检/写入`, 'success')
  } catch (error) { show(`导入配装失败: ${String(error)}`, 'error') } finally { busy.value = false }
}
async function importFileJSON(event) {
  const file = event.target.files?.[0]; event.target.value = ''
  if (!file) return
  try { await importPayload(await file.text()) } catch (error) { show(`读取文件失败: ${String(error)}`, 'error') }
}
async function loadLogs() {
  try { logsRecords.value = await SelectLogsSigilLoadouts() || []; selectedLogRecord.value = '0'; selectedLogPlayer.value = '0'; show(`已读取 ${logsRecords.value.length} 场 Logs 记录`, 'success') } catch (error) { show(`读取 Logs 失败: ${String(error)}`, 'error') }
}
function logRecordLabel(record) {
  const quest = record.questName && !String(record.questName).startsWith('未收录任务') ? ` · ${record.questName}` : ''
  return `${new Date(record.logTime).toLocaleString()}${quest}（${record.loadouts.length} 名玩家）`
}
function logPlayerCharacter(player) { return player?.characterName || player?.characterType || player?.loadout?.ownerCode || '未知角色' }
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
    selectedSlot.value = String(candidateContext?.slots?.[0]?.unitId || '')
    await selectSlot()
    return true
  }
  return false
}
async function importLogPlayer() {
  const player = logsRecords.value[Number(selectedLogRecord.value)]?.loadouts?.[Number(selectedLogPlayer.value)]
  if (!player?.loadout) return show('该 Logs 玩家没有完整配装快照', 'error')
  busy.value = true
  try {
    if (!await switchToLogPlayerCharacter(player)) throw new Error(`当前存档未找到 Logs 玩家对应角色（${player.loadout?.ownerCode || player.characterType || '未知'}）`)
    pendingLogImport.value = player.loadout
    show('已切换到对应角色；请选择目标槽位后导入该玩家配装', 'success')
  } catch (error) { show(`导入配装失败: ${String(error)}`, 'error') } finally { busy.value = false }
}
async function confirmLogImport() {
  if (!pendingLogImport.value) return
  await importPayload(JSON.stringify(pendingLogImport.value))
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
    show(`读取专精布局失败: ${String(error)}`, 'error')
  }
}
function masteryNodeActive(hash) { return displayMastery.value.includes(hash) }
function masteryCategoryActive(rank, cat) { return masterySummary.value?.ranks?.find(item => item.rank === rank)?.categories?.find(item => item.cat === cat)?.active || false }
function masteryNodes(rank, cat) {
  return rank.nodes.filter(item => item.cat === cat)
}
function masteryNodeDescription(node) {
  return node.hash === '1F52146F' ? '昏厥+4' : node.desc || node.name || '未收录效果'
}
function toggleMasteryCategory(cat) {
  masteryExpanded.value[cat] = !masteryExpanded.value[cat]
}
function masterySpecialization(cat) {
  return masterySummary.value?.ranks?.[0]?.categories?.find(item => item.cat === cat)?.specialization || ''
}
const masteryCategories = [
  { cat: 'SB_DEF', label: '觉醒' },
  { cat: 'SB_ATK', label: '真谛' },
  { cat: 'SB_LIMIT', label: '秘义' },
]

async function copyMasteryEffects() {
  if (!masteryPools.value.length) return show('请先读取角色槽位', 'error')
  const lines = []
  for (const category of masteryCategories) {
    lines.push(`${category.label}：${masterySpecialization(category.cat) || '—'}`)
    for (const rank of masteryPools.value) {
      const nodes = masteryNodes(rank, category.cat)
      lines.push(`${rank.rank === 'EX' ? 'EX阶' : `${rank.rank.slice(1)}阶段`}`)
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
    show('已复制专精效果与 hash', 'success')
  } catch (error) { show(`复制失败: ${String(error)}`, 'error') }
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
  if (!item?.attributeHash) return '空槽'
  const value = Number(item.value || 0)
  const suffix = item.unit === 'pct' ? '%' : ''
  return `${text(item.name || item.attributeHash)} Lv ${item.level}：+${value}${suffix}`
}

async function load() {
  if (!savePath.value.trim()) return show('请输入 SaveData 文件路径', 'error')
  busy.value = true
  try {
    groups.value = await LoadoutList(savePath.value.trim()) || []
    if (!groups.value.length) throw new Error('未找到已保存的角色配装')
    selectedCharacter.value = groups.value[0].charaHash
    importedDraft.value = null
    importedShare.value = null
    await loadContext()
    show(`已读取 ${groups.value.length} 个角色的配装`, 'success')
  } catch (error) { show(`读取配装失败: ${String(error)}`, 'error') } finally { busy.value = false }
}
async function loadContext() {
  if (!selectedCharacter.value) return
  busy.value = true
  try {
    context.value = await LoadoutEditContext(savePath.value.trim(), selectedCharacter.value)
    selectedSlot.value = String(context.value?.slots?.[0]?.unitId || '')
    await selectSlot()
  } catch (error) { show(`读取角色编辑资源失败: ${String(error)}`, 'error') } finally { busy.value = false }
}
async function selectSlot() {
  clearImportedDraft()
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
  if (!savePath.value.trim()) return
  try {
    const value = await LoadoutDetail(savePath.value.trim(), Number(item.unitId))
    detail.value = value
    await loadMasteryLayout()
    for (const warning of value.warnings || []) show(warning, 'error')
  } catch (error) { show(`读取槽位详情失败: ${String(error)}`, 'error') }
}
function toggleSigil(slotId) {
  slotId = Number(slotId)
  const values = form.value.sigilSlotIds
  const index = values.indexOf(slotId)
  if (index >= 0) values.splice(index, 1)
  else if (values.length < 12) values.push(slotId)
  else show('最多选择 12 个因子', 'error')
}
function toggleSkill(hash) {
  const values = form.value.skillHashes
  const index = values.indexOf(hash)
  if (index >= 0) values.splice(index, 1)
  else if (values.length < 4) values.push(hash)
  else show('最多选择 4 个技能', 'error')
}
async function preflight() {
  busy.value = true
  try {
    const report = await LoadoutCheckCompliance(savePath.value.trim(), form.value)
    result.value = { kind: '预检', report }
    show(report.writable ? '预检通过，可以写入' : `预检未通过：${report.message}`, report.writable ? 'success' : 'error')
  } catch (error) { show(`预检失败: ${String(error)}`, 'error') } finally { busy.value = false }
}
async function apply(copy) {
  if (!confirm(copy ? '将写入副本存档，是否继续？' : '将原地写入存档。请确认游戏已关闭并已备份存档。是否继续？')) return
  busy.value = true
  try {
    const output = copy ? `${savePath.value.trim()}.loadout.dat` : ''
    const request = importedDraft.value ? { changes: [{ ...form.value, constructedSigils: importedDraft.value.constructedSigils || [] }], importPayload: importedDraft.value.applyPayload || null } : null
    const written = request ? await LoadoutApplyWithResources(savePath.value.trim(), output, request) : await LoadoutApply(savePath.value.trim(), output, [form.value])
    importedDraft.value = null
    importedShare.value = null
    result.value = { kind: '写入完成', report: written }
    show(`写入并回读验证完成：${written.outputPath}`, 'success')
    await loadContext()
  } catch (error) { show(`写入失败: ${String(error)}`, 'error') } finally { busy.value = false }
}
</script>

<template>
  <div class="loadout-editor">
    <section class="section">
      <div class="section-title">完整离线配装 <span>仅操作存档，不连接游戏进程</span></div>
      <label class="field"><span>SaveData 路径</span><input v-model="savePath" placeholder="例如 C:\\...\\SaveData1.dat" @keyup.enter="load" /></label>
      <div class="actions"><button class="btn primary" :disabled="busy" @click="load">读取配装</button><button class="btn" :disabled="busy || !context" @click="exportCurrent">导出当前槽位</button><button class="btn" :disabled="busy || !context" @click="importFile?.click()">导入 JSON</button><button class="btn" :disabled="busy || !context" @click="loadLogs">读取 GBFR Logs</button><input ref="importFile" class="file-input" type="file" accept="application/json,.json" @change="importFileJSON" /></div>
      <div v-if="logsRecords.length" class="logs-import"><label class="field"><span>Logs 场次</span><select v-model="selectedLogRecord"><option v-for="(record,index) in logsRecords" :key="index" :value="String(index)">{{ logRecordLabel(record) }}</option></select></label><label class="field"><span>玩家</span><select v-model="selectedLogPlayer"><option v-for="(player,index) in (logsRecords[Number(selectedLogRecord)]?.loadouts || [])" :key="index" :value="String(index)">{{ player.playerName || '未命名玩家' }} / {{ logPlayerCharacter(player) }}</option></select></label><button class="btn" :disabled="busy" @click="importLogPlayer">导入该玩家完整配装</button></div>
      <p class="hint">写入会创建备份、修复 checksum 并回读验证。原地写入前必须关闭游戏；建议优先使用“写入副本”。</p>
    </section>

    <section v-if="groups.length" class="section">
      <div class="section-title">角色与目标槽位</div>
      <label class="field"><span>角色</span><select v-model="selectedCharacter" :disabled="busy" @change="loadContext"><option v-for="item in groups" :key="item.charaHash" :value="item.charaHash">{{ text(item.charaName) }} / {{ item.charaHash }}（{{ item.loadouts.length }} 个已保存配装）</option></select></label>
      <label class="field"><span>目标槽位</span><select v-model="selectedSlot" :disabled="busy" @change="selectSlot"><option v-for="item in slots" :key="item.unitId" :value="String(item.unitId)">{{ item.name || '空槽' }} · UnitID {{ item.unitId }}</option></select></label>
      <div v-if="pendingLogImport" class="actions"><button class="btn primary" :disabled="busy" @click="confirmLogImport">导入到当前目标槽位</button></div>
      <div v-if="displayedDetail" class="summary">
        <div class="section-title">当前槽位配装摘要 <span>{{ importedShare ? '待写入草稿' : '只读存档原始内容' }}</span></div>
        <p><b>角色：</b>{{ text(displayedDetail.charaName || selected?.charaName) }}（{{ text(displayedDetail.charaHash || selectedCharacter) }}）</p>
        <div><b>武器：</b>{{ text(displayedDetail.weaponName || displayedDetail.weaponHash) }}</div>
        <div v-if="displayedDetail.weapon"><b>武器强化：</b>Lv {{ displayedDetail.weapon.level || displayedDetail.weapon.xp || '—' }} / 上限突破 {{ displayedDetail.weapon.uncap }} / 幻晶 {{ displayedDetail.weapon.mirage }} / 觉醒 {{ displayedDetail.weapon.awakening }} / 超凡 {{ displayedDetail.weapon.transcendence }}</div>
        <div class="equipment-skills-summary">
          <div><b>角色上限突破：</b><ol v-if="displayOverLimit.length"><li v-for="item in displayOverLimit" :key="item.index">{{ formatOverLimit(item) }}</li></ol><span v-else>—</span></div>
          <div><b>武器技能：</b><ol v-if="displayWeaponSkills.length"><li v-for="item in displayWeaponSkills" :key="`${item.slot}-${item.traitHash}`">{{ item.slot + 1 }}. {{ text(item.name || item.traitHash) }} Lv {{ item.level }}</li></ol><span v-else>—</span></div>
          <div><b>武器祝福：</b><template v-if="displayedDetail.weapon?.wrightstone">{{ text(displayedDetail.weapon.wrightstone.name || displayedDetail.weapon.wrightstone.hash) }}<ol v-if="displayedDetail.weapon.wrightstone.traits?.length"><li v-for="item in displayedDetail.weapon.wrightstone.traits" :key="`${item.index}-${item.hash}`">{{ item.index + 1 }}. {{ text(item.name || item.hash) }} Lv {{ item.level }}</li></ol></template><span v-else>—</span></div>
          <div><b>技能：</b><ul v-if="displaySkills.length"><li v-for="item in displaySkills" :key="item.hash">{{ text(item.name || item.hash) }}（{{ item.hash }}）</li></ul><span v-else>—</span></div>
        </div>
        <div><b>召唤石：</b><ol v-if="displaySummons.length"><li v-for="(item,index) in displaySummons" :key="`${index}-${item.typeHash}`">{{ formatSummon(item, index) }}</li></ol><span v-else>—</span></div>
        <div><b>因子：</b><ol v-if="displaySigils.length"><li v-for="item in displaySigils" :key="`${item.index}-${item.slotId || item.hash}`">{{ formatSigil(item) }}</li></ol><span v-else>—</span></div>
        <div class="mastery-layout" v-if="masteryPools.length">
          <div class="mastery-tools"><b>专精激活图：</b><button class="btn copy-mastery" @click="copyMasteryEffects">复制效果对照</button></div>
          <div v-for="category in masteryCategories" :key="category.cat" class="mastery-category">
            <button class="mastery-heading" type="button" @click="toggleMasteryCategory(category.cat)">
              <span>{{ masteryExpanded[category.cat] ? '▼' : '▶' }} {{ category.label }}<template v-if="masterySpecialization(category.cat)">：{{ masterySpecialization(category.cat) }}</template></span>
            </button>
            <template v-if="masteryExpanded[category.cat]">
            <div v-for="rank in masteryPools" :key="rank.rank" class="mastery-rank">
              <span class="mastery-rank-label" :class="{ active: masteryCategoryActive(rank.rank, category.cat) }">{{ rank.rank === 'EX' ? 'EX阶' : `${rank.rank.slice(1)}阶段` }}</span>
              <span class="mastery-node-list">
                <span v-for="node in masteryNodes(rank, category.cat)" :key="node.hash" class="mastery-node" :class="{ active: masteryNodeActive(node.hash) }" :title="masteryNodeDescription(node)">
                  <b>{{ masteryNodeActive(node.hash) ? '■' : '□' }}</b>
                  <span>{{ masteryNodeDescription(node) }}</span>
                </span>
                <span v-if="!masteryNodes(rank, category.cat).length" class="mastery-empty">—</span>
              </span>
            </div>
            </template>
          </div>
          <small>亮蓝色：当前配装已激活；灰色：未激活。</small>
        </div>
        <p><b>角色强化：</b>{{ enhancementText() }}</p>
      </div>
      <label class="field"><span>配装名称</span><input v-model="form.name" maxlength="63" /></label>
    </section>

    <section v-if="context" class="section">
      <div class="section-title">导入到当前目标槽位 <span>{{ importedDraft ? '已载入完整配装草稿' : '请导入 JSON 或 GBFR Logs 玩家配装' }}</span></div>
      <p class="hint">流程：先读取目标存档并选择角色/槽位；再导入 JSON 或 Logs 玩家配装；最后预检并写入。导入内容会整体应用，不需要手动勾选武器、技能、因子或专精。</p>
      <div class="actions"><button class="btn" :disabled="busy || !importedDraft" @click="preflight">预检</button><button class="btn" :disabled="busy || !importedDraft" @click="apply(true)">写入副本</button><button class="btn danger" :disabled="busy || !importedDraft" @click="apply(false)">原地写入</button></div>
    </section>

    <section v-if="result" class="section result"><div class="section-title">{{ result.kind }}</div><pre>{{ JSON.stringify(result.report, null, 2) }}</pre></section>
  </div>
</template>

<style scoped>
.loadout-editor{display:flex;flex-direction:column;gap:14px}.section{padding:14px 16px;border:1px solid rgba(255,255,255,.08);border-radius:8px;background:rgba(255,255,255,.04)}.equipment-skills-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-top:8px}.mastery-layout{margin-top:10px}.mastery-tools{display:flex;align-items:center;justify-content:space-between;gap:10px}.copy-mastery{padding:4px 8px;font-size:.68rem}.mastery-category{margin:10px 0;padding:9px 10px;border-left:2px solid rgba(255,255,255,.14);background:rgba(255,255,255,.025)}.mastery-heading{display:block;width:100%;margin-bottom:6px;padding:0;border:0;background:transparent;color:rgba(255,255,255,.84);font:inherit;font-weight:600;text-align:left;cursor:pointer}.mastery-heading:hover{color:#67e8f9}.mastery-rank{display:flex;align-items:flex-start;gap:8px;min-height:25px}.mastery-rank-label{width:42px;flex:0 0 42px;padding-top:1px;color:rgba(255,255,255,.42);font-size:.7rem}.mastery-rank-label.active{color:#67e8f9}.mastery-node-list{display:flex;min-width:0;flex:1;flex-direction:column;gap:4px}.mastery-node{display:flex;gap:6px;color:rgba(255,255,255,.48);font-size:.72rem;line-height:1.4}.mastery-node b{flex:0 0 auto;color:rgba(255,255,255,.2);font-size:.86rem;line-height:1}.mastery-node.active{color:rgba(255,255,255,.82)}.mastery-node.active b{color:#38bdf8;text-shadow:0 0 7px rgba(56,189,248,.75)}.mastery-empty{color:rgba(255,255,255,.28);font-size:.72rem}.mastery-layout small{display:block;margin-top:5px;color:rgba(255,255,255,.4);font-size:.68rem}.logs-import{margin-top:12px;padding:10px 12px;border:1px solid rgba(251,191,36,.25);border-radius:6px;background:rgba(251,191,36,.04)}.file-input{display:none}.summary{margin-top:12px;padding:12px;border:1px solid rgba(103,232,249,.22);border-radius:6px;background:rgba(103,232,249,.045);color:rgba(255,255,255,.7);font-size:.73rem;line-height:1.55}.draft-summary{border-color:rgba(251,191,36,.32);background:rgba(251,191,36,.045)}.summary p{margin:8px 0}.summary ol,.summary ul{margin:5px 0 8px;padding-left:22px}.summary li{margin:3px 0}.section-title{display:flex;justify-content:space-between;gap:8px;color:rgba(255,255,255,.78);font-size:.8rem;font-weight:600}.section-title span{color:rgba(255,255,255,.4);font-weight:400}.field{display:flex;flex-direction:column;gap:5px;margin-top:10px;color:rgba(255,255,255,.5);font-size:.72rem}.field input,.field select,.field textarea,.grid select{box-sizing:border-box;width:100%;padding:7px 9px;border:1px solid rgba(255,255,255,.13);border-radius:6px;background:#2a2a2a;color:rgba(255,255,255,.88);font:inherit;font-size:.75rem}.field textarea{resize:vertical;font-family:ui-monospace,Consolas,monospace}.actions{display:flex;flex-wrap:wrap;gap:8px;margin-top:12px}.btn{padding:7px 12px;border:1px solid rgba(255,255,255,.15);border-radius:6px;background:rgba(255,255,255,.05);color:rgba(255,255,255,.8);font:600 .75rem inherit;cursor:pointer}.btn:disabled{opacity:.4;cursor:not-allowed}.primary{border-color:rgba(103,232,249,.35);color:#67e8f9}.danger{border-color:rgba(248,113,113,.45);color:#f87171}.hint{margin:10px 0 0;color:rgba(255,255,255,.42);font-size:.72rem;line-height:1.5}.grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px}.check{display:block;margin-top:7px;color:rgba(255,255,255,.72);font-size:.72rem;word-break:break-all}.check input{margin-right:6px}.result pre{margin:10px 0 0;max-height:300px;overflow:auto;white-space:pre-wrap;word-break:break-word;color:rgba(255,255,255,.65);font-size:.7rem}@media(max-width:760px){.grid,.equipment-skills-summary{grid-template-columns:1fr}}
</style>
