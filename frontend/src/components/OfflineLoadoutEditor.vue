<script setup>
import { computed, ref } from 'vue'
import { LoadoutApply, LoadoutApplyWithResources, LoadoutCheckCompliance, LoadoutDetail, LoadoutEditContext, LoadoutExportJSON, LoadoutImportJSON, LoadoutList } from '../../wailsjs/go/main/OfflineLoadoutService'
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
const importFile = ref(null)
const logsRecords = ref([])
const selectedLogRecord = ref('')
const selectedLogPlayer = ref('')

function emptyForm() {
  return { unitId: 0, expectCharaHash: '', op: 'write', name: '', weaponSlotId: 0, sigilSlotIds: [], skillHashes: [], weaponSkillHashes: [], masteryHashes: [] }
}
function show(message, type) { emit('status', message, type) }
function hex(value) { return `0x${(Number(value) >>> 0).toString(16).toUpperCase().padStart(8, '0')}` }
function text(value) { return value || '—' }
function formatSigil(item) {
  const secondary = item.secondaryTraitHash ? ` / ${text(item.secondaryTraitName || item.secondaryTraitHash)} Lv ${item.secondaryTraitLevel}` : ''
  return `${Number(item.index) + 1}. ${text(item.name || item.hash)} Lv ${item.level} · ${text(item.primaryTraitName || item.primaryTraitHash)} Lv ${item.primaryTraitLevel}${secondary}`
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
    const draft = await LoadoutImportJSON(savePath.value.trim(), context.value.charaHash, payload)
    importedDraft.value = draft
    form.value = { ...emptyForm(), unitId: Number(selectedSlot.value), expectCharaHash: context.value.charaHash, op: 'write', name: draft.name || form.value.name, weaponSlotId: Number(draft.weaponSlotId || 0), sigilSlotIds: (draft.sigilSlotIds || []).map(Number), skillHashes: draft.skillHashes || [], weaponSkillHashes: draft.weaponSkillHashes || [], masteryHashes: draft.masteryHashes || [] }
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
async function importLogPlayer() {
  const player = logsRecords.value[Number(selectedLogRecord.value)]?.loadouts?.[Number(selectedLogPlayer.value)]
  if (!player?.loadout) return show('该 Logs 玩家没有完整配装快照', 'error')
  await importPayload(JSON.stringify(player.loadout))
}
const character = computed(() => groups.value.find(item => item.charaHash === selectedCharacter.value))
const slots = computed(() => context.value?.slots || [])
const weapons = computed(() => context.value?.weapons || [])
const sigils = computed(() => context.value?.sigils || [])
const skills = computed(() => context.value?.skills || [])
const selected = computed(() => slots.value.find(item => Number(item.unitId) === Number(selectedSlot.value)))
const detailSigils = computed(() => detail.value?.sigils || [])
const detailSkills = computed(() => detail.value?.skills || [])
const detailMastery = computed(() => detail.value?.masteryHashes || [])
const detailOverLimit = computed(() => detail.value?.overLimit || [])
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
    selectSlot()
  } catch (error) { show(`读取角色编辑资源失败: ${String(error)}`, 'error') } finally { busy.value = false }
}
function selectSlot() {
  const item = selected.value
  if (!item) return
  if (importedDraft.value) {
    form.value = { ...form.value, unitId: Number(item.unitId), expectCharaHash: context.value.charaHash, op: 'write' }
  } else {
    form.value = {
      unitId: Number(item.unitId), expectCharaHash: context.value.charaHash, op: 'write', name: item.name || '',
      weaponSlotId: Number(item.weaponSlotId || 0), sigilSlotIds: (item.sigilSlotIds || []).filter(Boolean).map(Number),
      skillHashes: (item.skillHashes || []).filter(Boolean), weaponSkillHashes: (item.weaponSkillHashes || []).filter(Boolean), masteryHashes: (item.masteryHashes || []).filter(Boolean),
    }
  }
  result.value = null
  detail.value = null
  if (savePath.value.trim()) {
    LoadoutDetail(savePath.value.trim(), Number(item.unitId))
      .then(value => { detail.value = value })
      .catch(error => show(`读取槽位详情失败: ${String(error)}`, 'error'))
  }
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
      <div v-if="logsRecords.length" class="logs-import"><label class="field"><span>Logs 场次</span><select v-model="selectedLogRecord"><option v-for="(record,index) in logsRecords" :key="index" :value="String(index)">{{ new Date(record.logTime).toLocaleString() }}（{{ record.loadouts.length }} 名玩家）</option></select></label><label class="field"><span>玩家</span><select v-model="selectedLogPlayer"><option v-for="(player,index) in (logsRecords[Number(selectedLogRecord)]?.loadouts || [])" :key="index" :value="String(index)">{{ player.playerName || '未命名玩家' }} / {{ player.characterName || player.characterType || '未知角色' }}</option></select></label><button class="btn" :disabled="busy" @click="importLogPlayer">导入该玩家完整配装</button></div>
      <p class="hint">写入会创建备份、修复 checksum 并回读验证。原地写入前必须关闭游戏；建议优先使用“写入副本”。</p>
    </section>

    <section v-if="groups.length" class="section">
      <div class="section-title">角色与目标槽位</div>
      <label class="field"><span>角色</span><select v-model="selectedCharacter" :disabled="busy" @change="loadContext"><option v-for="item in groups" :key="item.charaHash" :value="item.charaHash">{{ text(item.charaName) }} / {{ item.charaHash }}（{{ item.loadouts.length }} 个已保存配装）</option></select></label>
      <label class="field"><span>目标槽位</span><select v-model="selectedSlot" :disabled="busy" @change="selectSlot"><option v-for="item in slots" :key="item.unitId" :value="String(item.unitId)">{{ item.name || '空槽' }} · UnitID {{ item.unitId }}</option></select></label>
      <div v-if="detail" class="summary">
        <div class="section-title">当前槽位配装摘要 <span>只读存档原始内容</span></div>
        <p><b>角色：</b>{{ text(detail.charaName || character?.charaName) }}（{{ text(detail.charaHash || selectedCharacter) }}）</p>
        <p><b>武器：</b>{{ text(detail.weaponName) }}<template v-if="detail.weapon"> · Lv {{ detail.weapon.xp || 0 }} XP / 上限突破 {{ detail.weapon.uncap }} / 幻晶 {{ detail.weapon.mirage }} / 觉醒 {{ detail.weapon.awakening }}</template></p>
        <div><b>因子：</b><ol v-if="detailSigils.length"><li v-for="item in detailSigils" :key="`${item.index}-${item.slotId}`">{{ formatSigil(item) }}</li></ol><span v-else>—</span></div>
        <div><b>技能：</b><ul v-if="detailSkills.length"><li v-for="item in detailSkills" :key="item.hash">{{ text(item.name) }}（{{ item.hash }}）</li></ul><span v-else>—</span></div>
        <div><b>专精：</b><ul v-if="detailMastery.length"><li v-for="item in detailMastery" :key="item">{{ item }}</li></ul><span v-else>—</span></div>
        <div><b>上限突破：</b><ol><li v-for="(item,index) in detailOverLimit" :key="index">{{ index + 1 }}. {{ formatOverLimit(item) }}</li></ol></div>
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
.loadout-editor{display:flex;flex-direction:column;gap:14px}.section{padding:14px 16px;border:1px solid rgba(255,255,255,.08);border-radius:8px;background:rgba(255,255,255,.04)}.logs-import{margin-top:12px;padding:10px 12px;border:1px solid rgba(251,191,36,.25);border-radius:6px;background:rgba(251,191,36,.04)}.file-input{display:none}.summary{margin-top:12px;padding:12px;border:1px solid rgba(103,232,249,.22);border-radius:6px;background:rgba(103,232,249,.045);color:rgba(255,255,255,.7);font-size:.73rem;line-height:1.55}.summary p{margin:8px 0}.summary ol,.summary ul{margin:5px 0 8px;padding-left:22px}.summary li{margin:3px 0}.section-title{display:flex;justify-content:space-between;gap:8px;color:rgba(255,255,255,.78);font-size:.8rem;font-weight:600}.section-title span{color:rgba(255,255,255,.4);font-weight:400}.field{display:flex;flex-direction:column;gap:5px;margin-top:10px;color:rgba(255,255,255,.5);font-size:.72rem}.field input,.field select,.field textarea,.grid select{box-sizing:border-box;width:100%;padding:7px 9px;border:1px solid rgba(255,255,255,.13);border-radius:6px;background:#2a2a2a;color:rgba(255,255,255,.88);font:inherit;font-size:.75rem}.field textarea{resize:vertical;font-family:ui-monospace,Consolas,monospace}.actions{display:flex;flex-wrap:wrap;gap:8px;margin-top:12px}.btn{padding:7px 12px;border:1px solid rgba(255,255,255,.15);border-radius:6px;background:rgba(255,255,255,.05);color:rgba(255,255,255,.8);font:600 .75rem inherit;cursor:pointer}.btn:disabled{opacity:.4;cursor:not-allowed}.primary{border-color:rgba(103,232,249,.35);color:#67e8f9}.danger{border-color:rgba(248,113,113,.45);color:#f87171}.hint{margin:10px 0 0;color:rgba(255,255,255,.42);font-size:.72rem;line-height:1.5}.grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px}.check{display:block;margin-top:7px;color:rgba(255,255,255,.72);font-size:.72rem;word-break:break-all}.check input{margin-right:6px}.result pre{margin:10px 0 0;max-height:300px;overflow:auto;white-space:pre-wrap;word-break:break-word;color:rgba(255,255,255,.65);font-size:.7rem}@media(max-width:760px){.grid{grid-template-columns:1fr}}
</style>
