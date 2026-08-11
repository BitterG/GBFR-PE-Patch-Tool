<script setup>
import { translate as t } from '../i18n'
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { AutoChatGetStatus, AutoChatSetConfig, AutoChatSetEnabled, AutoChatSendNow, AutoChatListTemplates, AutoChatSaveTemplate, AutoChatDeleteTemplate, AutoChatSendTemplate } from '../../wailsjs/go/main/App'

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
const tplForm = reactive({ id: '', name: '', text: '', modifiers: 0, key: 0, enabled: true })
const capturing = ref(false)
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
  // 主键限 F1-F12 / 字母 / 数字。
  const validMain =
    (k >= 0x70 && k <= 0x7B) || (k >= 65 && k <= 90) || (k >= 48 && k <= 57)
  if (validMain) {
    tplForm.modifiers = mods
    tplForm.key = k
    stopCapture()
  }
}

function addTemplate() {
  editing.value = true
  editingIdx.value = -1
  Object.assign(tplForm, { id: '', name: '', text: '', modifiers: 0, key: 0, enabled: true })
}

function editTemplate(idx) {
  const tpl = templates.value[idx]
  editing.value = true
  editingIdx.value = idx
  Object.assign(tplForm, { id: tpl.id, name: tpl.name, text: tpl.text, modifiers: tpl.modifiers, key: tpl.key, enabled: tpl.enabled })
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

function sendTemplate(idx) {
  const tpl = templates.value[idx]
  sending.value = true
  AutoChatSendTemplate(tpl.id)
    .then((s) => {
      applyStatus(s)
      emit('status', t('runtimeTools.autoChat.messages.templateSent'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { sending.value = false })
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
          <input v-model="intervalInput" type="number" min="0" class="edit-input"
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
          <button class="btn-batch auto-chat-tpl-add" @click="addTemplate" :disabled="editing">{{ t('runtimeTools.autoChat.addTemplate') }}</button>
        </div>

        <!-- 模板列表 -->
        <div v-if="templates.length" class="auto-chat-tpl-list">
          <div v-for="(tpl, idx) in templates" :key="tpl.id" class="auto-chat-tpl-item">
            <div class="auto-chat-tpl-info">
              <span class="auto-chat-tpl-name">{{ tpl.name }}</span>
              <span class="auto-chat-tpl-text">{{ tpl.text }}</span>
              <span class="auto-chat-tpl-hotkey" :class="{ off: !tpl.enabled }">
                {{ tpl.enabled ? hotkeyLabel(tpl.modifiers, tpl.key) : t('runtimeTools.autoChat.disabled') }}
              </span>
            </div>
            <div class="auto-chat-tpl-actions">
              <button class="btn-batch" @click="sendTemplate(idx)" :disabled="sending">{{ t('runtimeTools.autoChat.send') }}</button>
              <button class="btn-batch" @click="editTemplate(idx)" :disabled="editing">{{ t('runtimeTools.autoChat.edit') }}</button>
              <button class="btn-refresh" @click="removeTemplate(idx)" :disabled="editing">{{ t('runtimeTools.autoChat.delete') }}</button>
            </div>
          </div>
        </div>
        <div v-else class="auto-chat-tpl-empty">{{ t('runtimeTools.autoChat.noTemplates') }}</div>

        <!-- 编辑表单 -->
        <div v-if="editing" class="auto-chat-tpl-edit">
          <input v-model="tplForm.name" class="edit-input auto-chat-tpl-name-input" :placeholder="t('runtimeTools.autoChat.tplName')" />
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
</style>
