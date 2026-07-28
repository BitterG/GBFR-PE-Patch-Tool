<script setup>
import { ref, computed } from 'vue'
import { translate as t } from '../i18n'
import { FindSaveFiles, GetQuests, LoadSave, GetSaveCounters, UpdateSaveCounters } from '../../wailsjs/go/main/App'

const slots = ref([])
const quests = ref([])
const total = ref(0)
const loading = ref(false)
const savingCounters = ref(false)
const savePath = ref('')
const sortDesc = ref(true)
const likes = ref(0)
const challenges = ref(0)
const counterStatus = ref('')
const showConfirm = ref(false)

const sortedQuests = computed(() => {
  if (!sortDesc.value) return quests.value
  return [...quests.value].sort((a, b) => b.clears - a.clears)
})

async function scanSaves() {
  slots.value = await FindSaveFiles() || []
}

async function load(path) {
  loading.value = true
  savePath.value = path
  try {
    const [summary, qs, counters] = await Promise.all([LoadSave(path), GetQuests(path), GetSaveCounters(path)])
    quests.value = qs || []
    total.value = summary?.questTotalClears || 0
    likes.value = counters?.likes || 0
    challenges.value = counters?.challenges || total.value
    counterStatus.value = ''
  } catch (err) { console.error(err); counterStatus.value = t('saveEditor.error.readFailed') } finally { loading.value = false }
}

function requestSaveCounters() {
  if (!savePath.value || !Number.isInteger(likes.value) || !Number.isInteger(challenges.value) || likes.value < 0 || challenges.value < 0) {
    counterStatus.value = t('saveEditor.error.nonNegativeInteger')
    return
  }
  showConfirm.value = true
}

async function saveCounters() {
  showConfirm.value = false
  savingCounters.value = true
  counterStatus.value = ''
  try {
    const counters = await UpdateSaveCounters(savePath.value, likes.value, challenges.value)
    likes.value = counters.likes
    challenges.value = counters.challenges
    total.value = counters.challenges
    const [summary, qs] = await Promise.all([LoadSave(savePath.value), GetQuests(savePath.value)])
    quests.value = qs || []
    total.value = summary?.questTotalClears || counters.challenges
    counterStatus.value = t('saveEditor.status.written')
  } catch (err) {
    counterStatus.value = String(err || t('saveEditor.error.writeFailed'))
  } finally {
    savingCounters.value = false
  }
}

scanSaves()
</script>

<template>
  <div class="root">
    <!-- 存档选择 -->
    <div class="slots">
      <button v-for="s in slots" :key="s.index" class="slot-btn"
        :class="{ on: savePath === s.path }" @click="load(s.path)">
        {{ s.name }}
      </button>
      <button class="refresh" @click="scanSaves">{{ t('common.refresh') }}</button>
    </div>

    <div v-if="savePath && !loading" class="counter-editor">
      <label>
        <span>{{ t('saveEditor.likes') }}</span>
        <input v-model.number="likes" type="number" min="0" max="2147483647" />
      </label>
      <label>
        <span>{{ t('saveEditor.challenges') }}</span>
        <input v-model.number="challenges" type="number" min="0" max="4294967295" />
      </label>
      <button class="apply-counters" @click="requestSaveCounters" :disabled="savingCounters">
        {{ savingCounters ? t('common.writing') : t('saveEditor.applyCounters') }}
      </button>
      <span v-if="counterStatus" class="counter-status">{{ counterStatus }}</span>
    </div>

    <div v-if="showConfirm" class="confirm-backdrop" @click.self="showConfirm = false">
      <section class="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="counter-confirm-title">
        <h2 id="counter-confirm-title">{{ t('saveEditor.confirm.title') }}</h2>
        <p>{{ t('saveEditor.confirm.body') }}</p>
        <div class="confirm-actions">
          <button class="confirm-cancel" @click="showConfirm = false">{{ t('common.cancel') }}</button>
          <button class="confirm-apply" @click="saveCounters">{{ t('saveEditor.confirm.apply') }}</button>
        </div>
      </section>
    </div>

    <div v-if="loading" class="loading">{{ t('common.parsing') }}</div>

    <div v-else-if="quests.length" class="quests">
      <div class="head">
        <span>{{ t('saveEditor.questSummary', { quests: quests.length, total }) }}</span>
        <button class="refresh" @click="load(savePath)">{{ t('common.refresh') }}</button>
        <button class="sort" @click="sortDesc = !sortDesc">{{ sortDesc ? t('saveEditor.sortByCount') : t('saveEditor.defaultOrder') }}</button>
      </div>
      <div class="list">
        <div v-for="q in sortedQuests" :key="q.questId" class="row">
          <span class="id">{{ q.questId }}</span>
          <span class="name">{{ q.questNameCn || q.questName }}</span>
          <span class="count" :class="{ hot: q.clears > 100 }">{{ q.clears }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.root { display:flex; flex-direction:column; gap:10px; width:100%; max-width:720px; height:100%; min-height:0; margin:0 auto; padding-bottom:0; box-sizing:border-box; }
.slots { display:flex; gap:8px; flex-wrap:wrap; justify-content:center; align-items:center; }
.slot-btn {
  padding:10px 20px; border-radius:10px; border:1px solid rgba(255,255,255,0.1);
  background:rgba(255,255,255,0.04); color:rgba(255,255,255,0.45);
  font-size:0.82rem; font-family:inherit; cursor:pointer; transition:all 0.2s;
}
.slot-btn:hover { border-color:rgba(103,232,249,0.2); color:rgba(255,255,255,0.7); }
.slot-btn.on { border-color:rgba(103,232,249,0.4); background:rgba(103,232,249,0.1); color:#67e8f9; }
.refresh {
  padding:6px 14px; border-radius:6px; border:1px solid rgba(255,255,255,0.08);
  background:transparent; color:rgba(255,255,255,0.3); font-size:0.75rem; cursor:pointer;
}
.refresh:hover { color:rgba(255,255,255,0.6); border-color:rgba(255,255,255,0.15); }

.loading { text-align:center; color:#67e8f9; font-size:0.82rem; padding:16px; }
.counter-editor { display:grid; grid-template-columns:1fr 1fr auto; gap:8px; align-items:end; padding:10px; border:1px solid rgba(103,232,249,0.14); background:rgba(103,232,249,0.04); border-radius:8px; }
.counter-editor label { display:flex; flex-direction:column; gap:4px; min-width:0; }
.counter-editor label span, .counter-status { font-size:0.7rem; color:rgba(255,255,255,0.42); }
.counter-editor input { min-width:0; box-sizing:border-box; padding:7px 9px; border:1px solid rgba(255,255,255,0.14); border-radius:5px; background:rgba(255,255,255,0.06); color:#fff; font:0.8rem 'Courier New',monospace; }
.apply-counters { padding:8px 12px; border:1px solid rgba(103,232,249,0.3); border-radius:5px; background:rgba(103,232,249,0.1); color:#67e8f9; font-size:0.75rem; cursor:pointer; white-space:nowrap; }
.apply-counters:disabled { opacity:0.45; cursor:not-allowed; }
.counter-status { grid-column:1/-1; }
.confirm-backdrop { position:fixed; inset:0; z-index:10; display:flex; align-items:center; justify-content:center; padding:20px; background:rgba(0,0,0,0.62); }
.confirm-dialog { width:min(420px, 100%); padding:18px; border:1px solid rgba(103,232,249,0.24); border-radius:8px; background:#17222f; box-shadow:0 18px 48px rgba(0,0,0,0.55); }
.confirm-dialog h2 { margin:0 0 10px; color:#e8f8fb; font-size:0.95rem; font-weight:600; }
.confirm-dialog p { margin:0; color:rgba(255,255,255,0.66); font-size:0.8rem; line-height:1.65; }
.confirm-actions { display:flex; justify-content:flex-end; gap:8px; margin-top:16px; }
.confirm-actions button { padding:7px 14px; border-radius:5px; font-size:0.78rem; cursor:pointer; }
.confirm-cancel { border:1px solid rgba(255,255,255,0.15); background:transparent; color:rgba(255,255,255,0.62); }
.confirm-apply { border:1px solid rgba(103,232,249,0.35); background:rgba(103,232,249,0.14); color:#67e8f9; }

.quests { border-radius:12px; border:1px solid rgba(255,255,255,0.06); background:rgba(255,255,255,0.02); overflow:hidden; flex:1; min-height:0; display:flex; flex-direction:column; }
.head { display:flex; align-items:center; padding:10px 14px; background:rgba(255,255,255,0.03); border-bottom:1px solid rgba(255,255,255,0.05); gap:10px; }
.head span { font-size:0.75rem; color:rgba(255,255,255,0.35); flex:1; }
.sort {
  padding:3px 10px; border-radius:4px; border:1px solid rgba(255,255,255,0.1);
  background:transparent; color:rgba(255,255,255,0.3); font-size:0.7rem; cursor:pointer;
}
.sort:hover { color:#67e8f9; border-color:rgba(103,232,249,0.3); }

.list { flex:1; min-height:0; overflow-y:auto; scrollbar-width:thin; scrollbar-color:rgba(255,255,255,0.08) transparent; }
.row { display:flex; align-items:center; gap:8px; padding:7px 14px; border-bottom:1px solid rgba(255,255,255,0.02); }
.row:hover { background:rgba(255,255,255,0.02); }
.id { width:48px; font-size:0.68rem; color:rgba(255,255,255,0.2); font-family:'Courier New',monospace; flex-shrink:0; }
.name { flex:1; font-size:0.8rem; color:rgba(255,255,255,0.5); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.count { width:40px; text-align:right; font-size:0.8rem; font-weight:600; color:rgba(255,255,255,0.35); font-family:'Courier New',monospace; flex-shrink:0; }
.count.hot { color:#fbbf24; }
</style>
