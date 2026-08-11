<script setup>
import { translate as t } from '../i18n'
import { onBeforeUnmount, onMounted, reactive, ref, computed } from 'vue'
import { AutoChatGetStatus, AutoChatSetConfig, AutoChatSetEnabled, AutoChatSendNow, AutoChatListTemplates, AutoChatSaveTemplate, AutoChatDeleteTemplate, AutoChatSendTemplate, AutoChatExternalStatus, AutoChatExternalSetEnabled, AutoChatGroups, AutoChatSetActiveGroup } from '../../wailsjs/go/main/App'

const emit = defineEmits(['status'])

const status = reactive({
  enabled: false,
  message: '',
  intervalSec: 0,
  lastSendAtUnix: 0,
  lastError: '',
  sentCount: 0,
})
const loading = ref(false)
const sending = ref(false)
const messageInput = ref('')
const intervalInput = ref('')
let refreshTimer = 0

// ── 模板管理 ──
const templates = ref([])
const editing = ref(false)
const editingIdx = ref(-1)
const tplForm = reactive({ id: '', group: '', name: '', text: '', modifiers: 0, key: 0, enabled: true })
// 当前激活分组的模板（列表只显示激活分组；手动按钮发送不限分组）。
const visibleTemplates = computed(() =>
  (templates.value || []).filter((t) => (t.group || '') === (activeGroup.value || '')),
)
const capturing = ref(false)
const groups = ref([])
const activeGroup = ref('')
const groupDropdownOpen = ref(false)

function toggleGroupDropdown() {
  groupDropdownOpen.value = !groupDropdownOpen.value
}
function pickGroup(name) {
  groupDropdownOpen.value = false
  setActiveGroup(name || '')
}

function loadGroups() {
  AutoChatGroups()
    .then((res) => {
      const r = res || {}
      groups.value = Array.isArray(r.groups) ? r.groups : []
      activeGroup.value = r.active || ''
    })
    .catch(() => {})
}

function setActiveGroup(name) {
  AutoChatSetActiveGroup(name || '')
    .then((res) => {
      const r = res || {}
      groups.value = Array.isArray(r.groups) ? r.groups : []
      activeGroup.value = r.active || name || ''
      groupDropdownOpen.value = false
      loadTemplates()
    })
    .catch((err) => emit('status', String(err), 'error'))
}

function groupLabel(name) {
  return name || t('runtimeTools.autoChat.groupDefault')
}

// ── 外部接入 HTTP 服务 ──
const external = reactive({ running: false, enabled: false, port: 17395 })
const externalLoading = ref(false)

function loadExternalStatus() {
  AutoChatExternalStatus()
    .then((s) => { Object.assign(external, s || { running: false, enabled: false, port: 17395 }) })
    .catch(() => {})
}

function externalHelpText() {
  return (t('runtimeTools.autoChat.externalHelp') || '').replaceAll('{port}', String(external.port || 17395))
}

function toggleExternal() {
  externalLoading.value = true
  AutoChatExternalSetEnabled(!external.enabled, external.port)
    .then((s) => {
      Object.assign(external, s)
      emit('status', s.running
        ? t('runtimeTools.autoChat.messages.externalStarted')
        : t('runtimeTools.autoChat.messages.externalStopped'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { externalLoading.value = false })
}
const modNames = [
  { mask: 0x8, name: 'Win' },
  { mask: 0x4, name: 'Shift' },
  { mask: 0x2, name: 'Ctrl' },
  { mask: 0x1, name: 'Alt' },
]

function hotkeyLabel(mods, key) {
  const parts = []
  if (mods & 0x8) parts.push('Win')
  if (mods & 0x4) parts.push('Shift')
  if (mods & 0x2) parts.push('Ctrl')
  if (mods & 0x1) parts.push('Alt')
  let k
  if (key === 0x05) k = 'XButton1'
  else if (key === 0x06) k = 'XButton2'
  else if (key === 0x04) k = 'Middle'
  else if (key >= 0x60 && key <= 0x69) k = `Num${key - 0x60}`
  else if (key === 0x6A) k = 'Num*'
  else if (key === 0x6B) k = 'Num+'
  else if (key === 0x6D) k = 'Num-'
  else if (key === 0x6E) k = 'Num.'
  else if (key === 0x6F) k = 'Num/'
  else if (key >= 0x70 && key <= 0x7B) k = `F${key - 0x6F}`
  else if (key >= 65 && key <= 90) k = String.fromCharCode(key)
  else k = String(key)
  parts.push(k)
  return parts.join('+')
}

function loadTemplates() {
  AutoChatListTemplates()
    .then((list) => { templates.value = list || [] })
    .catch((err) => emit('status', String(err), 'error'))
}

function startCapture() {
  capturing.value = true
  tplForm.modifiers = 0
  tplForm.key = 0
  // 用 window 级监听捕获按键，避免依赖元素焦点。
  window.addEventListener('keydown', onKeydownCapture, true)
  window.addEventListener('mousedown', onMouseCapture, true)
}

function stopCapture() {
  if (capturing.value) {
    capturing.value = false
    window.removeEventListener('keydown', onKeydownCapture, true)
    window.removeEventListener('mousedown', onMouseCapture, true)
  }
}

// 鼠标侧键/中键捕获：button 3=XButton1(0x05)、4=XButton2(0x06)、1=中键(0x04)。
function onMouseCapture(e) {
  if (!capturing.value) return
  e.preventDefault()
  e.stopPropagation()
  let vk = 0
  if (e.button === 3) vk = 0x05 // XBUTTON1
  else if (e.button === 4) vk = 0x06 // XBUTTON2
  else if (e.button === 1) vk = 0x04 // MBUTTON
  if (vk) {
    tplForm.modifiers = 0
    tplForm.key = vk
    stopCapture()
  }
}

function onKeydownCapture(e) {
  if (!capturing.value) return
  e.preventDefault()
  e.stopPropagation()
  const mods = (e.ctrlKey ? 0x2 : 0) | (e.shiftKey ? 0x4 : 0) | (e.altKey ? 0x1 : 0) | (e.metaKey ? 0x8 : 0)
  const k = e.keyCode || e.which
  // 纯修饰键（Ctrl/Alt/Shift/Win）不作为主键，等待下一个键。
  if (k === 16 || k === 17 || k === 18 || k === 91 || k === 92) return
  // 主键限 F1-F12 / 字母 / 数字 / 小键盘（Num0-9 及运算符）。
  const validMain =
    (k >= 0x70 && k <= 0x7B) || // F1-F12
    (k >= 65 && k <= 90) || // A-Z
    (k >= 48 && k <= 57) || // 主键盘 0-9
    (k >= 0x60 && k <= 0x6F) // 小键盘 Num0-9 / * + - . /
  if (validMain) {
    tplForm.modifiers = mods
    tplForm.key = k
    stopCapture()
  }
}

function addTemplate() {
  editing.value = true
  editingIdx.value = -1
  Object.assign(tplForm, { id: '', group: activeGroup.value, name: '', text: '', modifiers: 0, key: 0, enabled: true })
}

function editTemplate(idx) {
  const tpl = templates.value[idx]
  editing.value = true
  editingIdx.value = idx
  Object.assign(tplForm, { id: tpl.id, group: tpl.group || '', name: tpl.name, text: tpl.text, modifiers: tpl.modifiers, key: tpl.key, enabled: tpl.enabled })
}

function cancelEdit() {
  editing.value = false
  stopCapture()
}

function saveTemplate() {
  if (!tplForm.name.trim() || !tplForm.text.trim()) {
    emit('status', t('runtimeTools.autoChat.messages.templateRequired'), 'error')
    return
  }
  if (!tplForm.key) {
    emit('status', t('runtimeTools.autoChat.messages.hotkeyRequired'), 'error')
    return
  }
  loading.value = true
  AutoChatSaveTemplate({ ...tplForm })
    .then((list) => {
      templates.value = list || []
      editing.value = false
      stopCapture()
      // 保存后自动切换到模板所在分组，确保刚保存的模板立即可见。
      if ((tplForm.group || '') !== (activeGroup.value || '')) {
        setActiveGroup(tplForm.group || '')
      } else {
        loadGroups()
      }
      emit('status', t('runtimeTools.autoChat.messages.templateSaved'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function removeTemplate(idx) {
  const id = templates.value[idx].id
  loading.value = true
  AutoChatDeleteTemplate(id)
    .then((list) => {
      templates.value = list || []
      emit('status', t('runtimeTools.autoChat.messages.templateDeleted'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function sendTemplateByIdx(idx) {
  const tpl = visibleTemplates.value[idx]
  if (!tpl) return
  sending.value = true
  AutoChatSendTemplate(tpl.id)
    .then((s) => {
      applyStatus(s)
      emit('status', t('runtimeTools.autoChat.messages.templateSent'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { sending.value = false })
}

function editTemplateByIdx(idx) {
  const tpl = visibleTemplates.value[idx]
  if (!tpl) return
  editing.value = true
  editingIdx.value = idx
  Object.assign(tplForm, { id: tpl.id, group: tpl.group || '', name: tpl.name, text: tpl.text, modifiers: tpl.modifiers, key: tpl.key, enabled: tpl.enabled })
}

function removeTemplateByIdx(idx) {
  const tpl = visibleTemplates.value[idx]
  if (!tpl) return
  const id = tpl.id
  loading.value = true
  AutoChatDeleteTemplate(id)
    .then((list) => {
      templates.value = list || []
      loadGroups()
      emit('status', t('runtimeTools.autoChat.messages.templateDeleted'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function formatTimestamp(unixMs) {
  if (!unixMs) return '—'
  const d = new Date(unixMs)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getHours()}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function applyStatus(s) {
  Object.assign(status, s || { enabled: false, message: '', intervalSec: 0, lastSendAtUnix: 0, lastError: '', sentCount: 0 })
}

function loadStatus() {
  AutoChatGetStatus()
    .then(applyStatus)
    .catch((err) => emit('status', String(err), 'error'))
}

function saveConfig() {
  const message = messageInput.value.trim()
  if (!message) {
    emit('status', t('runtimeTools.autoChat.messages.messageRequired'), 'error')
    return
  }
  const interval = parseInt(intervalInput.value, 10)
  if (isNaN(interval) || interval < 0) {
    emit('status', t('runtimeTools.autoChat.messages.intervalInvalid'), 'error')
    return
  }
  if (interval > 0 && interval < 3) {
    emit('status', t('runtimeTools.autoChat.messages.intervalMin'), 'error')
    return
  }
  loading.value = true
  AutoChatSetConfig(message, interval)
    .then((s) => {
      applyStatus(s)
      emit('status', t('runtimeTools.autoChat.messages.saved'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function setEnabled(enabled) {
  loading.value = true
  AutoChatSetEnabled(enabled)
    .then((s) => {
      applyStatus(s)
      if (enabled && s.enabled) {
        emit('status', t('runtimeTools.autoChat.messages.started'), 'success')
      } else if (!enabled && !s.enabled) {
        emit('status', t('runtimeTools.autoChat.messages.stopped'), 'success')
      }
      loadStatus()
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function sendNow() {
  // 免保存：直接发送输入框当前内容；空则用已保存配置。
  sending.value = true
  AutoChatSendNow(messageInput.value.trim())
    .then((s) => {
      applyStatus(s)
      emit('status', t('runtimeTools.autoChat.messages.sent'), 'success')
      loadStatus()
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { sending.value = false })
}

onMounted(() => {
  loadStatus()
  loadTemplates()
  loadGroups()
  loadExternalStatus()
  refreshTimer = setInterval(loadStatus, 2000)
})

onBeforeUnmount(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
  <div class="auto-chat">
    <div class="memory-card" :class="{ active: status.enabled }">
      <div class="memory-header">
        <span class="memory-title">{{ t('runtimeTools.autoChat.title') }}</span>
        <span class="info-dot" :title="t('runtimeTools.autoChat.notice')">!</span>
        <span class="memory-hint">{{ status.enabled ? t('runtimeTools.autoChat.running') : t('runtimeTools.autoChat.hint') }}</span>
      </div>

      <div class="memory-info">
        <span>{{ t('runtimeTools.autoChat.status') }}: {{ status.enabled ? t('runtimeTools.autoChat.enabled') : t('runtimeTools.autoChat.disabled') }}</span>
        <span v-if="status.sentCount > 0">{{ t('runtimeTools.autoChat.sentCount') }}: {{ status.sentCount }}</span>
        <span v-if="status.lastSendAtUnix">{{ t('runtimeTools.autoChat.lastSend') }}: {{ formatTimestamp(status.lastSendAtUnix) }}</span>
      </div>

      <div class="auto-chat-field">
        <label class="auto-chat-label">{{ t('runtimeTools.autoChat.message') }}</label>
        <textarea v-model="messageInput" class="auto-chat-textarea" rows="2"
          :placeholder="t('runtimeTools.autoChat.messagePlaceholder')" :disabled="status.enabled"></textarea>
      </div>

      <div class="auto-chat-field">
        <label class="auto-chat-label">{{ t('runtimeTools.autoChat.interval') }}</label>
        <div class="auto-chat-interval-row">
          <input v-model="intervalInput" type="number" min="3" class="edit-input"
            :placeholder="t('runtimeTools.autoChat.intervalPlaceholder')" :disabled="status.enabled" />
          <span class="auto-chat-unit">{{ t('runtimeTools.autoChat.ms') }}</span>
        </div>
        <span class="auto-chat-rate-limit">{{ t('runtimeTools.autoChat.rateLimitHint') }}</span>
      </div>

      <div class="memory-row">
        <button class="btn-batch" @click="saveConfig" :disabled="loading || status.enabled">{{ t('runtimeTools.autoChat.save') }}</button>
        <button class="btn-batch" @click="setEnabled(true)" :disabled="loading || status.enabled">{{ t('runtimeTools.autoChat.start') }}</button>
        <button class="btn-refresh" @click="setEnabled(false)" :disabled="loading || !status.enabled">{{ t('runtimeTools.autoChat.stop') }}</button>
        <button class="btn-batch" @click="sendNow" :disabled="sending">{{ sending ? t('runtimeTools.autoChat.sending') : t('runtimeTools.autoChat.sendNow') }}</button>
      </div>

      <div v-if="status.lastError" class="memory-bytes auto-chat-error">{{ t('runtimeTools.autoChat.lastError') }}: {{ status.lastError }}</div>

      <!-- 热键模板 -->
      <div class="auto-chat-field">
        <div class="auto-chat-tpl-header">
          <label class="auto-chat-label">{{ t('runtimeTools.autoChat.templates') }}</label>
          <!-- 分组下拉 + 添加热键 合并一行 -->
          <div class="auto-chat-tpl-header-right">
            <div class="auto-chat-group-select">
              <button class="auto-chat-group-trigger" @click="toggleGroupDropdown" :disabled="editing">
                <span class="auto-chat-group-trigger-label">{{ t('runtimeTools.autoChat.groupLabel') }}:</span>
                <span class="auto-chat-group-trigger-value">{{ groupLabel(activeGroup) }}</span>
                <span class="auto-chat-group-caret" :class="{ open: groupDropdownOpen }">▾</span>
              </button>
              <div v-if="groupDropdownOpen" class="auto-chat-group-menu">
                <button
                  v-for="g in groups"
                  :key="g.name || '__default__'"
                  class="auto-chat-group-menu-item"
                  :class="{ active: (g.name || '') === activeGroup }"
                  @click="pickGroup(g.name || '')"
                >
                  {{ groupLabel(g.name) }}
                  <span class="auto-chat-group-count">{{ g.templateCount }}</span>
                </button>
              </div>
            </div>
            <button class="btn-batch auto-chat-tpl-add" @click="addTemplate" :disabled="editing">{{ t('runtimeTools.autoChat.addHotkey') }}</button>
          </div>
        </div>

        <!-- 模板列表（只显示激活分组） -->
        <div v-if="visibleTemplates.length" class="auto-chat-tpl-list">
          <div v-for="(tpl, idx) in visibleTemplates" :key="tpl.id" class="auto-chat-tpl-item">
            <div class="auto-chat-tpl-info">
              <span class="auto-chat-tpl-name">{{ tpl.name }}</span>
              <span class="auto-chat-tpl-text">{{ tpl.text }}</span>
              <span class="auto-chat-tpl-hotkey" :class="{ off: !tpl.enabled }">
                {{ tpl.enabled ? hotkeyLabel(tpl.modifiers, tpl.key) : t('runtimeTools.autoChat.disabled') }}
              </span>
            </div>
            <div class="auto-chat-tpl-actions">
              <button class="btn-batch" @click="sendTemplateByIdx(idx)" :disabled="sending">{{ t('runtimeTools.autoChat.send') }}</button>
              <button class="btn-batch" @click="editTemplateByIdx(idx)" :disabled="editing">{{ t('runtimeTools.autoChat.edit') }}</button>
              <button class="btn-refresh" @click="removeTemplateByIdx(idx)" :disabled="editing">{{ t('runtimeTools.autoChat.delete') }}</button>
            </div>
          </div>
        </div>
        <div v-else class="auto-chat-tpl-empty">{{ t('runtimeTools.autoChat.noTemplates') }}</div>

        <!-- 编辑表单 -->
        <div v-if="editing" class="auto-chat-tpl-edit">
          <div class="auto-chat-tpl-edit-row">
            <input v-model="tplForm.name" class="edit-input auto-chat-tpl-name-input" :placeholder="t('runtimeTools.autoChat.tplName')" />
            <input v-model="tplForm.group" class="edit-input auto-chat-tpl-group-input" :placeholder="t('runtimeTools.autoChat.tplGroup')" />
          </div>
          <textarea v-model="tplForm.text" class="auto-chat-tpl-text-input" rows="3"
            :placeholder="t('runtimeTools.autoChat.tplText')"></textarea>
          <div class="auto-chat-hotkey-row">
            <button class="btn-batch" @click="startCapture" :disabled="capturing">
              {{ capturing ? t('runtimeTools.autoChat.pressKey') : (tplForm.key ? hotkeyLabel(tplForm.modifiers, tplForm.key) : t('runtimeTools.autoChat.captureHotkey')) }}
            </button>
            <span v-if="capturing" class="auto-chat-unit">{{ t('runtimeTools.autoChat.pressKeyHint') }}</span>
            <label class="auto-chat-tpl-enable">
              <input type="checkbox" v-model="tplForm.enabled" /> {{ t('runtimeTools.autoChat.enabled') }}
            </label>
          </div>
          <div class="auto-chat-tpl-actions">
            <button class="btn-batch" @click="saveTemplate" :disabled="loading">{{ t('runtimeTools.autoChat.save') }}</button>
            <button class="btn-refresh" @click="cancelEdit">{{ t('runtimeTools.autoChat.cancel') }}</button>
          </div>
        </div>
      </div>

      <!-- 外部接入 HTTP 服务 -->
      <div class="auto-chat-field">
        <div class="auto-chat-tpl-header">
          <label class="auto-chat-label">{{ t('runtimeTools.autoChat.externalTitle') }}</label>
          <span class="auto-chat-external-status" :class="{ on: external.running }">
            {{ external.running ? t('runtimeTools.autoChat.running') : t('runtimeTools.autoChat.disabled') }}
          </span>
        </div>
        <div class="auto-chat-external-row">
          <input v-model.number="external.port" type="number" min="1" max="65535" class="edit-input auto-chat-external-port"
            :disabled="external.enabled" :placeholder="t('runtimeTools.autoChat.externalPortPlaceholder')" />
          <button class="btn-batch" @click="toggleExternal" :disabled="externalLoading">
            {{ external.enabled ? t('runtimeTools.autoChat.stop') : t('runtimeTools.autoChat.externalStart') }}
          </button>
        </div>
        <pre class="auto-chat-external-help">{{ externalHelpText() }}</pre>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* ── 通用样式（与其他页面保持一致）── */
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
.memory-bytes { font-size:0.68rem; color:rgba(255,255,255,0.36); word-break:break-all; }
.info-dot { display:inline-flex; align-items:center; justify-content:center; width:14px; height:14px; border-radius:50%; background:rgba(165,180,252,0.25); color:#a5b4fc; font-size:0.6rem; font-weight:700; cursor:help; }
.btn-batch { padding:6px 14px; border-radius:6px; border:1px solid rgba(165,180,252,0.3); background:rgba(165,180,252,0.1); color:#a5b4fc; font-size:0.78rem; font-weight:600; cursor:pointer; transition:background 0.2s; white-space:nowrap; }
.btn-batch:not(:disabled):hover { background:rgba(165,180,252,0.2); }
.btn-batch:disabled { opacity:0.4; cursor:not-allowed; }
.btn-refresh, .btn-sort { padding:6px 14px; border-radius:6px; border:1px solid rgba(255,255,255,0.12); background:rgba(255,255,255,0.05); color:rgba(255,255,255,0.5); font-size:0.78rem; font-weight:600; cursor:pointer; transition:background 0.2s; }
.btn-refresh:hover, .btn-sort:hover { background:rgba(255,255,255,0.1); color:rgba(255,255,255,0.7); }
.btn-refresh:disabled, .btn-sort:disabled { opacity:0.4; cursor:not-allowed; }
.edit-input { flex:1; padding:8px 14px; border-radius:8px; border:1px solid rgba(255,255,255,0.15); background:rgba(255,255,255,0.07); color:#fff; font-size:0.95rem; font-family:inherit; outline:none; transition:border-color 0.2s; }
.edit-input:focus { border-color:rgba(165,180,252,0.5); }
.edit-input:disabled { opacity:0.5; }

.auto-chat {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: 100%;
}
.auto-chat .memory-card {
  width: 100%;
  box-sizing: border-box;
}

.auto-chat-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 8px;
}

.auto-chat-label {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.6);
}

.auto-chat-textarea {
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 13px;
  border: 1px solid rgba(255, 255, 255, 0.15);
  background: rgba(255, 255, 255, 0.07);
  color: #fff;
  font-family: inherit;
  outline: none;
  transition: border-color 0.2s;
}

.auto-chat-textarea:focus {
  border-color: rgba(165, 180, 252, 0.5);
}

.auto-chat-textarea:disabled {
  opacity: 0.5;
}

.auto-chat-interval-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.auto-chat-interval-row .edit-input {
  width: 140px;
}

.auto-chat-unit {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.5);
}

.auto-chat-error {
  color: #fca5a5;
}

.auto-chat-rate-limit {
  font-size: 11px;
  color: rgba(255, 255, 255, 0.4);
}

/* ── 模板管理 ── */
.auto-chat-tpl-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.auto-chat-tpl-header-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.auto-chat-tpl-add {
  font-size: 12px;
  padding: 4px 10px;
}

.auto-chat-tpl-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 6px;
}

.auto-chat-tpl-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  padding: 8px 10px;
  background: rgba(255, 255, 255, 0.04);
  max-width: 100%;
  min-width: 0;
  box-sizing: border-box;
}

.auto-chat-tpl-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.auto-chat-tpl-name {
  font-size: 13px;
  font-weight: 600;
  color: #e5e7eb;
}

.auto-chat-tpl-text {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.6);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
  min-width: 0;
}

.auto-chat-tpl-hotkey {
  font-size: 11px;
  color: #86efac;
  font-family: monospace;
}

.auto-chat-tpl-hotkey.off {
  color: rgba(255, 255, 255, 0.4);
}

.auto-chat-tpl-actions {
  display: flex;
  gap: 6px;
}

.auto-chat-tpl-actions .btn-batch,
.auto-chat-tpl-actions .btn-refresh {
  font-size: 11px;
  padding: 3px 8px;
}

.auto-chat-tpl-empty {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.4);
  padding: 8px 0;
}

.auto-chat-tpl-edit {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
  border: 1px dashed rgba(255, 255, 255, 0.18);
  border-radius: 8px;
  padding: 10px;
}

.auto-chat-hotkey-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.auto-chat-tpl-enable {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: rgba(255, 255, 255, 0.7);
}

.auto-chat-tpl-name-input {
  flex: none;
  width: 100%;
  max-width: 240px;
}
.auto-chat-tpl-text-input {
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 13px;
  border: 1px solid rgba(255, 255, 255, 0.15);
  background: rgba(255, 255, 255, 0.07);
  color: #fff;
  font-family: inherit;
  outline: none;
  transition: border-color 0.2s;
}
.auto-chat-tpl-text-input:focus {
  border-color: rgba(165, 180, 252, 0.5);
}

/* ── 外部接入 ── */
.auto-chat-external-status {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.4);
}
.auto-chat-external-status.on {
  color: #86efac;
}
.auto-chat-external-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.auto-chat-external-port {
  flex: none;
  width: 120px;
}
.auto-chat-external-help {
  font-size: 11px;
  line-height: 1.5;
  color: rgba(255, 255, 255, 0.45);
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  padding: 8px 10px;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}

/* ── 热键分组下拉 ── */
.auto-chat-group-select {
  position: relative;
  margin-top: 6px;
  max-width: 320px;
}
.auto-chat-group-trigger {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 6px 12px;
  border-radius: 6px;
  border: 1px solid rgba(255, 255, 255, 0.15);
  background: rgba(255, 255, 255, 0.07);
  color: rgba(255, 255, 255, 0.8);
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}
.auto-chat-group-trigger:hover {
  background: rgba(255, 255, 255, 0.12);
  border-color: rgba(165, 180, 252, 0.4);
}
.auto-chat-group-trigger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.auto-chat-group-trigger-label {
  color: rgba(255, 255, 255, 0.5);
  font-size: 12px;
}
.auto-chat-group-trigger-value {
  color: #67e8f9;
  font-weight: 600;
}
.auto-chat-group-caret {
  margin-left: auto;
  color: rgba(255, 255, 255, 0.5);
  transition: transform 0.15s;
}
.auto-chat-group-caret.open {
  transform: rotate(180deg);
}
.auto-chat-group-menu {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 20;
  background: rgba(20, 28, 40, 0.98);
  border: 1px solid rgba(255, 255, 255, 0.15);
  border-radius: 8px;
  padding: 4px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
  max-height: 240px;
  overflow-y: auto;
}
.auto-chat-group-menu-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 7px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: rgba(255, 255, 255, 0.7);
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
  text-align: left;
}
.auto-chat-group-menu-item:hover {
  background: rgba(255, 255, 255, 0.1);
  color: rgba(255, 255, 255, 0.95);
}
.auto-chat-group-menu-item.active {
  color: #67e8f9;
  background: rgba(103, 232, 249, 0.12);
}
.auto-chat-group-count {
  font-size: 11px;
  color: rgba(255, 255, 255, 0.4);
}
.auto-chat-tpl-edit-row {
  display: flex;
  gap: 8px;
}
.auto-chat-tpl-group-input {
  flex: none;
  width: 140px;
}
</style>
