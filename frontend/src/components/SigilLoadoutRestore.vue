<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
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
  idle: '未开始',
  record: `录制中 ${entries.value.length}/${MAX_ENTRIES}`,
  write: `写入中 ${writeIndex.value}/${entries.value.length}`,
}[mode.value]))
function nameFor(map, value) { return map.get(value >>> 0) || `未知 ${hex(value)}` }
function detail(entry) {
  const secondary = entry.secondaryTraitHash
    ? ` / ${nameFor(traitNames.value, entry.secondaryTraitHash)} Lv ${entry.secondaryTraitLevel}`
    : ''
  return `${nameFor(sigilNames.value, entry.sigilHash)} Lv ${entry.sigilLevel} · ${nameFor(traitNames.value, entry.primaryTraitHash)} Lv ${entry.primaryTraitLevel}${secondary}`
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
      show(`已录制 ${entries.value.length}/${MAX_ENTRIES}: ${nameFor(sigilNames.value, current.sigilHash)}`, 'success')
      if (entries.value.length === MAX_ENTRIES) {
        stop()
        show('录制完成，已达到 12 条', 'success')
      }
      return
    }

    if (mode.value === 'write' && writeIndex.value < entries.value.length) {
      const target = entries.value[writeIndex.value]
      await SigilMemoryUpdate(target)
      writeIndex.value++
      show(`已写入 ${writeIndex.value}/${entries.value.length}: ${nameFor(sigilNames.value, target.sigilHash)}`, 'success')
      lastSeen = ''
      if (writeIndex.value === entries.value.length) {
        stop()
        show('配装写入完成', 'success')
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
    show('录制已开始：游戏内选中首个因子后自动记录，切换因子继续记录', 'success')
  } catch (error) { show(String(error), 'error') }
}

async function startWrite() {
  if (mode.value !== 'idle') return
  if (!entries.value.length) { show('请先录制或导入配装', 'error'); return }
  try {
    await enableReader()
    writeIndex.value = 0
    lastSeen = ''
    mode.value = 'write'
    timer = window.setInterval(poll, 50)
    show('写入已开始：游戏内选中第一个因子后自动写入；每切换一次因子写入下一条', 'success')
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
  show(`已选择 ${importedComment.value || '玩家'} 的 ${entries.value.length} 条配装`, 'success')
}

async function importLogs() {
  if (mode.value !== 'idle') return
  try {
    logsRecords.value = await SelectLogsSigilLoadouts() || []
    if (!logsRecords.value.length) throw new Error('日志未包含可用玩家配装')
    selectedLogsRecord.value = '0'
    logsDialogOpen.value = true
  } catch (error) { show(`GBFR Logs 导入失败: ${String(error)}`, 'error') }
}

function confirmLogsRecord() {
  selectLogsRecord()
  logsDialogOpen.value = false
  show(`已读取第 ${Number(selectedLogsRecord.value) + 1} 条记录，含 ${logsLoadouts.value.length} 名玩家；可切换团队玩家`, 'success')
}

function closeLogsDialog() { logsDialogOpen.value = false }

function exportJSON() {
  if (!entries.value.length) { show('没有可导出的配装', 'error'); return }
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
      throw new Error('不是支持的因子或完整配装 JSON 文件')
    }
    if (!sourceEntries.length || sourceEntries.length > MAX_ENTRIES || !sourceEntries.every(validEntry)) {
      throw new Error('配装因子条目无效，数量必须为 1 到 12 条')
    }
    entries.value = sourceEntries.map(snapshot)
    sharedLoadout.value = complete ? data : null
    importWarnings.value = []
    exportVersion.value = complete ? `gbfr-loadout v${data.version}` : (typeof data.loadoutVersion === 'string' ? data.loadoutVersion : '')
    exportComment.value = complete ? (typeof data.name === 'string' ? data.name : '') : (typeof data.comment === 'string' ? data.comment : '')
    importedVersion.value = exportVersion.value
    importedComment.value = exportComment.value
    writeIndex.value = 0
    show(`已导入 ${entries.value.length} 条配装`, 'success')
  } catch (error) { show(`导入失败: ${String(error)}`, 'error') }
}

onMounted(async () => {
  try {
    const result = await SigilMemoryGetOptions()
    options.sigils = result.sigils || []
    options.traits = result.traits || []
  } catch (error) { show(`读取因子数据失败: ${String(error)}`, 'error') }
})
onBeforeUnmount(stop)
</script>

<template>
  <div class="loadout">
    <div v-if="logsDialogOpen" class="logs-dialog-mask" @click.self="closeLogsDialog">
      <section class="logs-dialog" role="dialog" aria-modal="true" aria-labelledby="logs-dialog-title">
        <div id="logs-dialog-title" class="section-title">选择 GBFR Logs 记录 <span>{{ logsRecords.length }} 条可用</span></div>
        <div class="logs-records">
          <label v-for="(record, index) in logsRecords" :key="index" class="logs-record" :class="{ selected: selectedLogsRecord === String(index) }">
            <input v-model="selectedLogsRecord" type="radio" name="logs-record" :value="String(index)" />
            <span>{{ new Date(record.logTime).toLocaleString() }}</span>
            <small>{{ record.loadouts.length }} 名玩家</small>
          </label>
        </div>
        <div class="actions">
          <button class="btn" @click="closeLogsDialog">取消</button>
          <button class="btn btn-logs" @click="confirmLogsRecord">确认导入</button>
        </div>
      </section>
    </div>
    <section class="section">
      <div class="section-title">因子配装复出 <span>{{ modeText }}</span></div>
      <div class="actions">
        <button class="btn btn-record" :disabled="mode !== 'idle'" @click="startRecord">开始录制</button>
        <button class="btn btn-write" :disabled="mode !== 'idle' || !entries.length" @click="startWrite">开始写入</button>
        <button class="btn" :disabled="mode === 'idle'" @click="stop">停止</button>
        <button class="btn" :disabled="mode !== 'idle' || !entries.length" @click="exportJSON">导出 JSON</button>
        <button class="btn" :disabled="mode !== 'idle'" @click="chooseImport">导入 JSON</button>
        <button class="btn btn-logs" :disabled="mode !== 'idle'" @click="importLogs">导入 GBFR Logs</button>
        <button class="btn btn-danger" :disabled="mode !== 'idle' || !entries.length" @click="clearEntries">清空</button>
        <input ref="fileInput" class="file-input" type="file" accept="application/json,.json" @change="importJSON" />
      </div>
      <div v-if="logsLoadouts.length" class="logs-picker">
        <label>
          <span>GBFR Logs 团队玩家</span>
          <select v-model="selectedLogsLoadout" :disabled="mode !== 'idle'" @change="selectLogsLoadout">
            <option v-for="(loadout, index) in logsLoadouts" :key="index" :value="String(index)">
              {{ loadout.playerName || '未命名玩家' }}<template v-if="loadout.characterName"> / {{ loadout.characterName }}</template>（{{ loadout.entries.length }} 条）
            </option>
          </select>
        </label>
      </div>
      <div class="export-fields">
        <label>
          <span>配装版本号</span>
          <input v-model="exportVersion" :disabled="mode !== 'idle'" maxlength="80" placeholder="例如 1.0.0" />
        </label>
        <label>
          <span>注释</span>
          <textarea v-model="exportComment" :disabled="mode !== 'idle'" maxlength="500" rows="2" placeholder="配装用途、角色或说明..." />
        </label>
      </div>
      <div v-if="importedVersion || importedComment" class="imported-meta">
        <span v-if="importedVersion">导入版本：{{ importedVersion }}</span>
        <span v-if="importedComment">导入注释：{{ importedComment }}</span>
      </div>
      <div v-if="importWarnings.length" class="import-warnings">
        <strong>导入警告</strong>
        <span v-for="(warning, index) in importWarnings" :key="index">{{ warning }}</span>
      </div>
      <p class="hint">录制：被录制角色装好12个因子进入 持有物>因子 按角色筛选，焦点第1行点录制后焦点依次向下滑动到第12行完成。
        写入：与录制同理，被写入角色佩戴12个未使用的任意因子，持有物>因子 按角色筛选，从第1行点击写入滑到12行。
      注意事项：不要装备两个及以上个完全相同的因子，不要滑太快，会导致遗漏。完整 `gbfr-loadout` 文件和 GBFR Logs 快照会保留武器、技能、召唤石、专精、上限突破等草稿数据；本模块当前只会实际写入因子，其他字段不会直接写入未经验证的存档布局。
      </p>
    </section>

    <section class="section">
      <div class="section-title">配装内容 <span>{{ entries.length }}/{{ MAX_ENTRIES }}</span></div>
      <div v-if="!entries.length" class="empty">尚无配装</div>
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
