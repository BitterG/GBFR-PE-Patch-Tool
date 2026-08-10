<script setup>
import { translate as t } from '../i18n'
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { AutoChatGetStatus, AutoChatSetConfig, AutoChatSetEnabled, AutoChatSendNow } from '../../wailsjs/go/main/App'

const emit = defineEmits(['status'])

const status = reactive({
  enabled: false,
  message: '',
  intervalMs: 0,
  lastSendAtUnix: 0,
  lastError: '',
  sentCount: 0,
})
const loading = ref(false)
const sending = ref(false)
const messageInput = ref('')
const intervalInput = ref('')
let refreshTimer = 0

function formatTimestamp(unixMs) {
  if (!unixMs) return '—'
  const d = new Date(unixMs)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getHours()}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function applyStatus(s) {
  Object.assign(status, s || { enabled: false, message: '', intervalMs: 0, lastSendAtUnix: 0, lastError: '', sentCount: 0 })
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
  sending.value = true
  AutoChatSendNow()
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
      </div>

      <div class="memory-row">
        <button class="btn-batch" @click="saveConfig" :disabled="loading || status.enabled">{{ t('runtimeTools.autoChat.save') }}</button>
        <button class="btn-batch" @click="setEnabled(true)" :disabled="loading || status.enabled">{{ t('runtimeTools.autoChat.start') }}</button>
        <button class="btn-refresh" @click="setEnabled(false)" :disabled="loading || !status.enabled">{{ t('runtimeTools.autoChat.stop') }}</button>
        <button class="btn-batch" @click="sendNow" :disabled="sending">{{ sending ? t('runtimeTools.autoChat.sending') : t('runtimeTools.autoChat.sendNow') }}</button>
      </div>

      <div v-if="status.lastError" class="memory-bytes auto-chat-error">{{ t('runtimeTools.autoChat.lastError') }}: {{ status.lastError }}</div>
    </div>
  </div>
</template>

<style scoped>
.auto-chat {
  display: flex;
  flex-direction: column;
  gap: 14px;
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
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.06);
  color: #e5e7eb;
  font-family: inherit;
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
</style>
