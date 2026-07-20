<script setup>
import { reactive, ref, computed, onMounted } from 'vue'
import { AutoDetect, SetExePath, GetStatus, PatchFile, BackupFile, RestoreFile } from '../../wailsjs/go/main/App'
import { WindowMinimise, Quit } from '../../wailsjs/runtime/runtime'
import SigilGenerator from './SigilGenerator.vue'
import SigilMemoryGenerator from './SigilMemoryGenerator.vue'
import SigilLoadoutRestore from './SigilLoadoutRestore.vue'
import WrightstoneGenerator from './WrightstoneGenerator.vue'
import WrightstoneMemoryGenerator from './WrightstoneMemoryGenerator.vue'
import SaveEditor from './SaveEditor.vue'
import BadgeUnlock from './BadgeUnlock.vue'
import CharaStats from './CharaStats.vue'
import MiscTools from './MiscTools.vue'
import MonsterEnhance from './MonsterEnhance.vue'
import OverLimit from './OverLimit.vue'
import SummonEditor from './SummonEditor.vue'
import LanguageSettings from './LanguageSettings.vue'
import { hasStoredLanguage, language, translateText } from '../i18n'

const state = reactive({
  exePath: '',
  fileExists: false,
  fileSize: 0,
  backupExists: false,
  backupSize: 0,
  patches: [],
})

const activeTab = ref(hasStoredLanguage() ? 'sigil' : 'language')
const manualPath = ref('')
const patchValues = reactive({}) // { patchID: 'value' }
const isLoaded = ref(false)
const isDetecting = ref(false)
const patchingID = ref('')
const forceBackup = ref(false)
const saveStatus = ref('')
const statusType = ref('')

onMounted(() => {
  isDetecting.value = true
  AutoDetect()
    .then((path) => {
      isDetecting.value = false
      if (path) {
        state.exePath = path
        manualPath.value = path
        return loadFile(path)
      }
    })
    .catch(() => { isDetecting.value = false })
})

function loadFile(path) {
  return GetStatus(path).then((info) => {
    Object.assign(state, info)
    ;(info.patches || []).forEach(p => {
      if (p.state === 'patched') patchValues[p.id] = String(p.currentValue)
      else if (!patchValues[p.id]) patchValues[p.id] = ''
    })
    isLoaded.value = true
    showStatus('文件加载成功', 'success')
  })
}

function applyManualPath() {
  const p = manualPath.value.trim()
  if (!p) { showStatus('请输入文件路径', 'error'); return }
  SetExePath(p)
    .then((info) => {
      Object.assign(state, info)
      ;(info.patches || []).forEach(p => {
        if (p.state === 'patched') patchValues[p.id] = String(p.currentValue)
        else if (!patchValues[p.id]) patchValues[p.id] = ''
      })
      isLoaded.value = true
      showStatus('文件加载成功', 'success')
    })
    .catch((err) => showStatus(String(err), 'error'))
}

function refreshStatus() {
  return GetStatus(state.exePath).then((info) => {
    Object.assign(state, info)
    ;(info.patches || []).forEach(p => {
      if (p.state === 'patched') patchValues[p.id] = String(p.currentValue)
    })
  })
}

function applyPatch(patchID) {
  const v = parseInt(patchValues[patchID])
  if (isNaN(v) || v < 0) { showStatus('请输入有效数值', 'error'); return }
  patchingID.value = patchID
  PatchFile(patchID, v)
    .then(() => refreshStatus())
    .then(() => { patchingID.value = ''; showStatus('补丁写入成功', 'success') })
    .catch((err) => { patchingID.value = ''; showStatus('补丁失败: ' + (err || '未知错误'), 'error') })
}

function backup() {
  BackupFile(forceBackup.value)
    .then(() => refreshStatus())
    .then(() => showStatus('备份创建成功', 'success'))
    .catch((err) => showStatus('备份失败: ' + (err || '未知错误'), 'error'))
}

function restore() {
  RestoreFile()
    .then(() => refreshStatus())
    .then(() => showStatus('文件已恢复', 'success'))
    .catch((err) => showStatus('恢复失败: ' + (err || '未知错误'), 'error'))
}

const CARD_COLORS = {
  mission: { bg: 'linear-gradient(135deg, rgba(124,58,237,0.25) 0%, rgba(249,212,35,0.1) 100%)', shadow: 'rgba(124,58,237,0.18)' },
  likes:   { bg: 'linear-gradient(135deg, rgba(245,158,11,0.25) 0%, rgba(249,212,35,0.1) 100%)', shadow: 'rgba(245,158,11,0.18)' },
}

const CARD_HINTS = {
  mission: '此修改不影响存档',
  likes: '被点赞后生效（影响存档）',
}

function showStatus(msg, type) {
  saveStatus.value = translateText(String(msg)); statusType.value = type
  setTimeout(() => { saveStatus.value = '' }, 3000)
}

</script>

<template>
  <div class="app-window">
    <div class="starfield" aria-hidden="true">
      <span class="stars stars-1"></span>
      <span class="stars stars-2"></span>
      <span class="stars stars-3"></span>
    </div>
    <div class="titlebar" style="--wails-draggable:drag">
      <div class="titlebar-left">
        <span class="titlebar-title">GBFR 存档修改工具</span>
        <transition name="fade">
          <span v-if="saveStatus" class="titlebar-status" :class="statusType">
            {{ statusType === 'success' ? '●' : '✕' }} {{ saveStatus }}
          </span>
        </transition>
      </div>
      <div class="titlebar-controls" style="--wails-draggable:no-drag">
        <button class="win-btn minimize" @click="WindowMinimise" title="最小化">
          <svg width="10" height="1" viewBox="0 0 10 1"><rect width="10" height="1.5" rx="0.75" fill="currentColor"/></svg>
        </button>
        <button class="win-btn close" @click="Quit" title="关闭">
          <svg width="10" height="10" viewBox="0 0 10 10"><line x1="1" y1="1" x2="9" y2="9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><line x1="9" y1="1" x2="1" y2="9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
        </button>
      </div>
    </div>

    <div class="tab-bar" style="--wails-draggable:no-drag">
      <button class="tab-btn language-tab" :class="{ active: activeTab === 'language' }" @click="activeTab = 'language'">
        {{ language === 'zh' ? '语言/LANG' : 'Language' }}
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'sigil' }" @click="activeTab = 'sigil'">
        因子生成
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'sigilMemory' }" @click="activeTab = 'sigilMemory'">
        因子生成-新
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'sigilLoadout' }" @click="activeTab = 'sigilLoadout'">
        因子配装复出
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'wrightstone' }" @click="activeTab = 'wrightstone'">
        祝福生成
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'wrightstoneMemory' }" @click="activeTab = 'wrightstoneMemory'">
        祝福生成-新
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'chara' }" @click="activeTab = 'chara'">
        角色次数统计
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'save' }" @click="activeTab = 'save'">
        副本次数
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'badge' }" @click="activeTab = 'badge'">
        解锁称号
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'misc' }" @click="activeTab = 'misc'">
        杂项
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'summon' }" @click="activeTab = 'summon'">
        召唤石
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'overlimit' }" @click="activeTab = 'overlimit'">
        上限突破
      </button>
      <button class="tab-btn" :class="{ active: activeTab === 'monster' }" @click="activeTab = 'monster'">
        怪物增强（未修复）
      </button>
    </div>

    <main v-if="activeTab === 'patch'" class="container" style="--wails-draggable:no-drag">
      <div class="path-section">
        <div class="path-label">
          <span v-if="isDetecting">正在扫描 Steam 安装路径...</span>
          <span v-else-if="isLoaded" class="path-found">已定位游戏文件</span>
          <span v-else>输入 granblue_fantasy_relink.exe 路径</span>
        </div>
        <div class="path-input-row">
          <input v-model="manualPath" type="text" class="path-input"
            placeholder="粘贴 exe 文件完整路径..." @keyup.enter="applyManualPath" />
          <button class="btn-path-confirm" @click="applyManualPath" :disabled="!manualPath.trim()">确定</button>
        </div>
      </div>

      <transition name="slide-up">
        <div v-if="isLoaded" class="data-panel">
          <div v-if="state.exePath" class="path-bar">
            <span class="path-text" :title="state.exePath">{{ state.exePath }}</span>
            <span class="file-size">{{ (state.fileSize / 1024 / 1024).toFixed(1) }} MB</span>
          </div>

          <!-- 每个补丁点一张卡片 -->
          <div v-for="p in state.patches" :key="p.id" class="data-card"
            :style="{ background: (CARD_COLORS[p.id]||CARD_COLORS.mission).bg, boxShadow: '0 4px 20px '+(CARD_COLORS[p.id]||CARD_COLORS.mission).shadow }">
            <div class="card-header">
              <span class="card-label">{{ p.name }}</span>
              <span v-if="CARD_HINTS[p.id]" class="card-hint">{{ CARD_HINTS[p.id] }}</span>
              <span v-if="p.state==='original'" class="state-badge original">未补丁</span>
              <span v-else-if="p.state==='patched'" class="state-badge patched">已补丁</span>
              <span v-else class="state-badge unknown">未知</span>
            </div>
            <div v-if="p.state==='patched'" class="card-detail">
              当前值: {{ p.currentValue }} (0x{{ p.currentValue.toString(16).toUpperCase() }})
            </div>
            <div class="card-edit-row">
              <input v-model="patchValues[p.id]" type="number" min="0" class="edit-input" placeholder="输入数值" />
              <button class="btn-patch" @click="applyPatch(p.id)"
                :disabled="patchingID === p.id || !patchValues[p.id] || isNaN(parseInt(patchValues[p.id]))">
                {{ patchingID === p.id ? '写入中...' : '应用' }}
              </button>
            </div>
          </div>

          <!-- 备份/恢复 -->
          <div class="backup-section">
            <div class="backup-row">
              <button class="btn-secondary" @click="backup">备份</button>
              <button class="btn-secondary restore" @click="restore" :disabled="!state.backupExists">恢复</button>
            </div>
            <label class="force-label">
              <input type="checkbox" v-model="forceBackup" />
              <span>强制覆盖已有备份</span>
            </label>
            <div v-if="state.backupExists" class="backup-info">备份: {{ (state.backupSize / 1024 / 1024).toFixed(1) }} MB (仅 exe)</div>
          </div>
        </div>
      </transition>

            <transition name="fade">
        <div v-if="!isLoaded && !isDetecting" class="placeholder">
          <p>未自动检测到游戏，请手动输入 exe 路径</p>
          <p class="placeholder-tip">建议先备份原始文件再进行补丁操作</p>
        </div>
      </transition>
      <div class="footer-hint"><a href="https://github.com/BitterG/GBFR-PE-Patch-Tool" target="_blank" class="footer-link">github.com/BitterG/GBFR-PE-Patch-Tool</a></div>
    </main>

    <main v-else-if="activeTab === 'sigil'" class="container" style="--wails-draggable:no-drag">
      <SigilGenerator @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'sigilMemory'" class="container" style="--wails-draggable:no-drag">
      <SigilMemoryGenerator @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'sigilLoadout'" class="container" style="--wails-draggable:no-drag">
      <SigilLoadoutRestore @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'wrightstone'" class="container" style="--wails-draggable:no-drag">
      <WrightstoneGenerator @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'wrightstoneMemory'" class="container" style="--wails-draggable:no-drag">
      <WrightstoneMemoryGenerator @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'chara'" class="container" style="--wails-draggable:no-drag">
      <CharaStats @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'save'" class="container" style="--wails-draggable:no-drag">
      <SaveEditor />
    </main>

    <main v-else-if="activeTab === 'badge'" class="container" style="--wails-draggable:no-drag">
      <BadgeUnlock @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'misc'" class="container" style="--wails-draggable:no-drag">
      <MiscTools @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'summon'" class="container summon-container" style="--wails-draggable:no-drag">
      <SummonEditor @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'overlimit'" class="container" style="--wails-draggable:no-drag">
      <OverLimit @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'monster'" class="container" style="--wails-draggable:no-drag">
      <MonsterEnhance @status="showStatus" />
    </main>

    <main v-else-if="activeTab === 'language'" class="container" style="--wails-draggable:no-drag">
      <LanguageSettings />
    </main>
  </div>
</template>

<style scoped>
.app-window { position:relative; display:flex; flex-direction:column; height:100vh; overflow:hidden; background:radial-gradient(ellipse at bottom, #12365c 0%, #06111f 52%, #020711 100%); border-radius:10px; box-shadow:0 8px 40px rgba(0,0,0,0.6); }
.starfield { position:absolute; inset:0; overflow:hidden; pointer-events:none; z-index:0; opacity:0.34; }
.stars { position:absolute; top:0; left:0; border-radius:50%; background:transparent; color:#d8efff; opacity:0.58; }
.stars::after { content:""; position:absolute; top:100vh; left:0; border-radius:50%; background:transparent; color:#fff; }
.stars-1,
.stars-1::after { width:1px; height:1px; box-shadow: 6vw 9vh currentColor, 18vw 32vh currentColor, 28vw 14vh currentColor, 41vw 47vh currentColor, 54vw 7vh currentColor, 69vw 28vh currentColor, 82vw 19vh currentColor, 94vw 43vh currentColor, 12vw 71vh currentColor, 36vw 88vh currentColor, 61vw 64vh currentColor, 76vw 83vh currentColor, 90vw 72vh currentColor, 24vw 58vh currentColor, 49vw 79vh currentColor; animation: starDrift 46s linear infinite; }
.stars-2,
.stars-2::after { width:2px; height:2px; box-shadow: 9vw 22vh currentColor, 31vw 53vh currentColor, 45vw 31vh currentColor, 63vw 12vh currentColor, 79vw 55vh currentColor, 96vw 26vh currentColor, 15vw 86vh currentColor, 58vw 91vh currentColor, 87vw 78vh currentColor; opacity:0.55; animation: starDrift 82s linear infinite; }
.stars-3,
.stars-3::after { width:3px; height:3px; box-shadow: 21vw 18vh currentColor, 72vw 36vh currentColor, 39vw 73vh currentColor, 92vw 89vh currentColor, 8vw 61vh currentColor; opacity:0.38; animation: starDrift 130s linear infinite; }
.titlebar, .tab-bar, .container { position:relative; z-index:1; }
@keyframes starDrift { from { transform:translateY(0); } to { transform:translateY(-100vh); } }

.tab-bar {
  display: flex;
  gap: 0;
  padding: 5px 8px;
  background: rgba(18,26,38,0.95);
  border-bottom: 1px solid rgba(255,255,255,0.06);
  flex-shrink: 0;
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: none;
  overscroll-behavior-x: contain;
}
.tab-bar::-webkit-scrollbar { display:none; }
.tab-btn {
  padding: 5px 7px;
  border-radius: 6px;
  border: none;
  background: transparent;
  color: rgba(255,255,255,0.58);
  font-size: 0.66rem;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
  white-space: nowrap;
  flex-shrink: 1;
}
.tab-btn:hover { color: rgba(255,255,255,0.82); background: rgba(255,255,255,0.06); }
.tab-btn:focus-visible { outline:1px solid rgba(103,232,249,0.58); outline-offset:-1px; }
.tab-btn.active { color: #67e8f9; background: rgba(103,232,249,0.12); }
.language-tab { margin-left: 0; }
.titlebar { display:flex; align-items:center; justify-content:space-between; height:38px; padding:0 6px 0 14px; background:rgba(18,26,38,0.95); border-bottom:1px solid rgba(255,255,255,0.06); flex-shrink:0; user-select:none; }
.titlebar-left { display:flex; align-items:center; gap:8px; }
.titlebar-title { font-size:0.8rem; font-weight:600; color:rgba(255,255,255,0.76); letter-spacing:0.5px; }
.titlebar-controls { display:flex; align-items:center; gap:2px; }
.win-btn { width:32px; height:28px; border:none; border-radius:6px; background:transparent; color:rgba(255,255,255,0.45); cursor:pointer; display:flex; align-items:center; justify-content:center; transition:background 0.15s,color 0.15s; }
.win-btn.minimize:hover { background:rgba(255,255,255,0.1); color:rgba(255,255,255,0.9); }
.win-btn.close:hover { background:rgba(239,68,68,0.8); color:#fff; }
.titlebar-status { font-size:0.68rem; font-weight:600; padding:2px 8px; border-radius:20px; white-space:nowrap; }
.titlebar-status.success { color:#4ade80; background:rgba(34,197,94,0.15); }
.titlebar-status.error { color:#f87171; background:rgba(239,68,68,0.15); }

.container { flex:1; overflow-y:auto; max-width:720px; width:100%; margin:0 auto; padding:20px 20px 40px; box-sizing:border-box; display:flex; flex-direction:column; align-items:center; gap:14px; scrollbar-width:none; }
.summon-container { max-width:none; align-items:stretch; padding-left:28px; padding-right:28px; }
.container::-webkit-scrollbar { display:none; }

.path-section { width:100%; }
.path-label { font-size:0.78rem; color:rgba(255,255,255,0.4); margin-bottom:8px; }
.path-found { color:#4ade80; }
.path-input-row { display:flex; gap:8px; }
.path-input { flex:1; padding:10px 14px; border-radius:10px; border:1px solid rgba(255,255,255,0.15); background:rgba(255,255,255,0.07); color:#fff; font-size:0.85rem; font-family:'Courier New',monospace; outline:none; transition:border-color 0.2s; }
.path-input::placeholder { color:rgba(255,255,255,0.22); }
.path-input:focus { border-color:rgba(103,232,249,0.5); background:rgba(255,255,255,0.1); }
.btn-path-confirm { padding:10px 18px; border-radius:10px; border:1px solid rgba(103,232,249,0.3); background:rgba(103,232,249,0.1); color:#67e8f9; font-size:0.85rem; font-weight:600; cursor:pointer; transition:background 0.2s,transform 0.15s; }
.btn-path-confirm:not(:disabled):hover { background:rgba(103,232,249,0.2); transform:scale(1.02); }
.btn-path-confirm:disabled { opacity:0.4; cursor:not-allowed; }

.path-bar { width:100%; box-sizing:border-box; padding:8px 14px; border-radius:10px; background:rgba(255,255,255,0.05); border:1px solid rgba(255,255,255,0.08); display:flex; align-items:center; justify-content:space-between; gap:8px; }
.path-text { font-size:0.72rem; color:rgba(255,255,255,0.4); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; font-family:'Courier New',monospace; flex:1; }
.file-size { font-size:0.68rem; color:rgba(255,255,255,0.3); flex-shrink:0; }

.data-panel { width:100%; display:flex; flex-direction:column; gap:12px; }
.data-card { border-radius:16px; padding:16px 18px; border:1px solid rgba(255,255,255,0.08); transition:transform 0.2s ease; display:flex; flex-direction:column; gap:8px; }
.data-card:hover { transform:translateY(-2px); }
.card-header { display:flex; align-items:center; justify-content:space-between; }
.card-label { font-size:0.88rem; font-weight:600; color:rgba(255,255,255,0.65); letter-spacing:1px; }
.card-hint { font-size:0.68rem; color:rgba(255,255,255,0.25); margin-left:4px; }
.card-detail { font-size:0.75rem; color:rgba(255,255,255,0.45); font-family:'Courier New',monospace; }

.state-badge { font-size:0.72rem; font-weight:700; padding:3px 10px; border-radius:20px; letter-spacing:0.5px; }
.state-badge.original { color:#67e8f9; background:rgba(103,232,249,0.15); }
.state-badge.patched { color:#4ade80; background:rgba(34,197,94,0.15); }
.state-badge.unknown { color:#fbbf24; background:rgba(251,191,36,0.15); }

.card-edit-row { display:flex; gap:8px; align-items:center; }
.edit-input { flex:1; padding:8px 14px; border-radius:8px; border:1px solid rgba(255,255,255,0.15); background:rgba(255,255,255,0.07); color:#fff; font-size:0.95rem; font-family:inherit; outline:none; transition:border-color 0.2s; }
.edit-input::placeholder { color:rgba(255,255,255,0.22); }
.edit-input:focus { border-color:rgba(255,255,255,0.4); background:rgba(255,255,255,0.12); }
.edit-input::-webkit-outer-spin-button, .edit-input::-webkit-inner-spin-button { -webkit-appearance:none; margin:0; }

.btn-patch { padding:8px 20px; border-radius:8px; border:1px solid rgba(165,180,252,0.3); background:rgba(165,180,252,0.1); color:#a5b4fc; font-size:0.85rem; font-weight:600; cursor:pointer; transition:background 0.2s,transform 0.15s; white-space:nowrap; }
.btn-patch:not(:disabled):hover { background:rgba(165,180,252,0.2); transform:scale(1.02); }
.btn-patch:disabled { opacity:0.4; cursor:not-allowed; }

.backup-section { padding:14px 18px; border-radius:16px; background:rgba(255,255,255,0.04); border:1px solid rgba(255,255,255,0.06); display:flex; flex-direction:column; gap:10px; }
.backup-row { display:flex; gap:10px; }
.btn-secondary { flex:1; padding:10px 0; border-radius:10px; border:1px solid rgba(255,255,255,0.12); background:rgba(40,48,64,0.8); color:rgba(255,255,255,0.6); font-size:0.85rem; font-weight:600; cursor:pointer; display:flex; align-items:center; justify-content:center; gap:6px; transition:color 0.2s,border-color 0.2s,transform 0.15s; }
.btn-secondary:not(:disabled):hover { color:#67e8f9; border-color:rgba(103,232,249,0.35); transform:scale(1.02); }
.btn-secondary.restore:not(:disabled):hover { color:#fbbf24; border-color:rgba(251,191,36,0.35); }
.btn-secondary:disabled { opacity:0.4; cursor:not-allowed; }
.force-label { display:flex; align-items:center; gap:6px; font-size:0.75rem; color:rgba(255,255,255,0.35); cursor:pointer; }
.force-label input[type="checkbox"] { accent-color:#67e8f9; }
.backup-info { font-size:0.72rem; color:rgba(255,255,255,0.3); font-family:'Courier New',monospace; }

.placeholder { margin-top:40px; color:rgba(255,255,255,0.25); text-align:center; font-size:0.88rem; line-height:1.8; }
.placeholder-tip { font-size:0.78rem; color:rgba(255,255,255,0.18); margin-top:8px; }

.footer-hint { width:100%; text-align:center; font-size:0.72rem; color:rgba(255,255,255,0.2); margin-top:auto; padding-top:16px; }
.footer-link { color:rgba(255,255,255,0.25); text-decoration:none; transition:color 0.2s; }
.footer-link:hover { color:rgba(103,232,249,0.6); }

.fade-enter-active, .fade-leave-active { transition:opacity 0.3s ease; }
.fade-enter-from, .fade-leave-to { opacity:0; }
.slide-up-enter-active { transition:all 0.4s cubic-bezier(0.25,0.46,0.45,0.94); }
.slide-up-enter-from { opacity:0; transform:translateY(24px); }

</style>
