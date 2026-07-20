<script setup>
import { ref, computed } from 'vue'
import { FindSaveFiles, GetBadgeList, UnlockAllBadges, SetBadge } from '../../wailsjs/go/main/App'
import { language } from '../i18n'

const emit = defineEmits(['status'])

const slots = ref([])
const savePath = ref('')
const loading = ref(false)
const working = ref(false)
const badges = ref([])
const markViewed = ref(false)
const search = ref('')
const tab = ref('all') // all | unlocked | locked
const showConfirm = ref(false)

// ── 虚拟滚动 ──
const ROW_H = 40
const VIEW_H = 420
const BUFFER = 6
const scrollTop = ref(0)

function badgeName(b) {
  const zh = b.nameZh || b.nameEn || ('#' + b.id)
  const en = b.nameEn || b.nameZh || ('#' + b.id)
  return language.value === 'zh' ? zh : en
}

const stats = computed(() => {
  const total = badges.value.length
  const unlocked = badges.value.reduce((n, b) => n + (b.unlocked ? 1 : 0), 0)
  return { total, unlocked, locked: total - unlocked }
})

const filtered = computed(() => {
  const kw = search.value.trim().toLowerCase()
  return badges.value.filter((b) => {
    if (tab.value === 'unlocked' && !b.unlocked) return false
    if (tab.value === 'locked' && b.unlocked) return false
    if (!kw) return true
    return (b.nameZh || '').toLowerCase().includes(kw)
      || (b.nameEn || '').toLowerCase().includes(kw)
      || String(b.id).includes(kw)
  })
})

const totalHeight = computed(() => filtered.value.length * ROW_H)
const startIndex = computed(() => Math.max(0, Math.floor(scrollTop.value / ROW_H) - BUFFER))
const endIndex = computed(() => Math.min(filtered.value.length, Math.ceil((scrollTop.value + VIEW_H) / ROW_H) + BUFFER))
const visible = computed(() => filtered.value.slice(startIndex.value, endIndex.value))
const offsetY = computed(() => startIndex.value * ROW_H)

function onScroll(e) { scrollTop.value = e.target.scrollTop }

async function scanSaves() {
  slots.value = await FindSaveFiles() || []
}

async function load(path) {
  loading.value = true
  savePath.value = path
  scrollTop.value = 0
  try {
    badges.value = await GetBadgeList(path) || []
  } catch (err) {
    badges.value = []
    emit('status', String(err || '读取存档失败'), 'error')
  } finally {
    loading.value = false
  }
}

function applyResult(result) {
  // 后端只回 status，不回完整列表；本地按刚才的写入意图刷新，再重载确保一致
  if (result && result.backupPath) {
    emit('status', `已写入，备份：${result.backupPath}`, 'success')
  }
}

async function toggleOne(b) {
  if (working.value || !savePath.value) return
  working.value = true
  const next = !b.unlocked
  try {
    const result = await SetBadge(savePath.value, b.id, next, markViewed.value)
    b.unlocked = next
    if (next && markViewed.value) b.viewed = true
    applyResult(result)
  } catch (err) {
    emit('status', String(err || '操作失败'), 'error')
  } finally {
    working.value = false
  }
}

function requestUnlockAll() {
  if (!savePath.value) { emit('status', '请先选择存档', 'error'); return }
  showConfirm.value = true
}

async function unlockAll() {
  showConfirm.value = false
  if (working.value || !savePath.value) return
  working.value = true
  try {
    const result = await UnlockAllBadges(savePath.value, markViewed.value)
    badges.value = await GetBadgeList(savePath.value) || []
    if (result && result.changed === 0) {
      emit('status', '全部称号已经解锁', 'success')
    } else {
      applyResult(result)
    }
  } catch (err) {
    emit('status', String(err || '解锁失败'), 'error')
  } finally {
    working.value = false
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
      <button class="refresh" @click="scanSaves">刷新</button>
    </div>

    <div v-if="loading" class="loading">解析中...</div>

    <template v-else-if="savePath && badges.length">
      <!-- 总计 + 操作 -->
      <div class="summary">
        <div class="stat"><strong>{{ stats.unlocked }} / {{ stats.total }}</strong><span>已解锁称号</span></div>
        <label class="viewed-opt"><input v-model="markViewed" type="checkbox"> 同时标记已查看</label>
        <button class="unlock-all" :disabled="working || stats.locked === 0" @click="requestUnlockAll">
          {{ working ? '处理中…' : stats.locked === 0 ? '已全部解锁' : '一键全解锁' }}
        </button>
      </div>

      <!-- 标签页 + 搜索 -->
      <div class="toolbar">
        <div class="tabs">
          <button class="tab" :class="{ on: tab === 'all' }" @click="tab = 'all'; scrollTop = 0">全部 {{ stats.total }}</button>
          <button class="tab" :class="{ on: tab === 'unlocked' }" @click="tab = 'unlocked'; scrollTop = 0">已解锁 {{ stats.unlocked }}</button>
          <button class="tab" :class="{ on: tab === 'locked' }" @click="tab = 'locked'; scrollTop = 0">未解锁 {{ stats.locked }}</button>
        </div>
        <input v-model="search" class="search" type="text" placeholder="搜索称号名称或 ID" @input="scrollTop = 0" />
      </div>

      <!-- 虚拟滚动列表 -->
      <div class="list" :style="{ height: VIEW_H + 'px' }" @scroll="onScroll">
        <div v-if="!filtered.length" class="empty">没有匹配的称号</div>
        <div v-else class="spacer" :style="{ height: totalHeight + 'px' }">
          <div class="rows" :style="{ transform: 'translateY(' + offsetY + 'px)' }">
            <div v-for="b in visible" :key="b.id" class="row" :class="{ unlocked: b.unlocked }" :style="{ height: ROW_H + 'px' }">
              <span class="id">{{ b.id }}</span>
              <span class="name" :title="badgeName(b)">{{ badgeName(b) }}</span>
              <span class="badge-state" :class="{ on: b.unlocked }">{{ b.unlocked ? '已解锁' : '未解锁' }}</span>
              <button class="toggle" :class="{ locked: !b.unlocked }" :disabled="working" @click="toggleOne(b)">
                {{ b.unlocked ? '取消' : '解锁' }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </template>

    <div v-else-if="savePath && !loading" class="loading">该存档未读到称号数据</div>

    <!-- 一键全解锁确认 -->
    <div v-if="showConfirm" class="confirm-backdrop" @click.self="showConfirm = false">
      <section class="confirm-dialog" role="dialog" aria-modal="true">
        <h2>确认解锁全部称号</h2>
        <p>请先完全退出游戏。将解锁全部 {{ stats.total }} 个有效称号，并自动备份当前存档。不会修改称号奖励领取状态。确认继续吗？</p>
        <div class="confirm-actions">
          <button class="confirm-cancel" @click="showConfirm = false">取消</button>
          <button class="confirm-apply" @click="unlockAll">确认解锁</button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.root { display:flex; flex-direction:column; gap:10px; width:100%; max-width:720px; height:100%; min-height:0; margin:0 auto; box-sizing:border-box; }
.slots { display:flex; gap:8px; flex-wrap:wrap; justify-content:center; align-items:center; }
.slot-btn { padding:10px 20px; border-radius:10px; border:1px solid rgba(255,255,255,0.1); background:rgba(255,255,255,0.04); color:rgba(255,255,255,0.45); font-size:0.82rem; font-family:inherit; cursor:pointer; transition:all 0.2s; }
.slot-btn:hover { border-color:rgba(103,232,249,0.2); color:rgba(255,255,255,0.7); }
.slot-btn.on { border-color:rgba(103,232,249,0.4); background:rgba(103,232,249,0.1); color:#67e8f9; }
.refresh { padding:6px 14px; border-radius:6px; border:1px solid rgba(255,255,255,0.08); background:transparent; color:rgba(255,255,255,0.3); font-size:0.75rem; cursor:pointer; }
.refresh:hover { color:rgba(255,255,255,0.6); border-color:rgba(255,255,255,0.15); }
.loading { text-align:center; color:#67e8f9; font-size:0.82rem; padding:16px; }

.summary { display:flex; align-items:center; gap:16px; padding:12px 14px; border:1px solid rgba(103,232,249,0.14); background:rgba(103,232,249,0.04); border-radius:8px; }
.stat { display:flex; flex-direction:column; gap:3px; }
.stat strong { color:#67e8f9; font-size:1.15rem; }
.stat span { font-size:0.72rem; color:rgba(255,255,255,0.5); }
.viewed-opt { margin-left:auto; font-size:0.75rem; color:rgba(255,255,255,0.6); display:flex; align-items:center; gap:5px; cursor:pointer; }
.unlock-all { padding:8px 16px; border:1px solid rgba(103,232,249,0.3); border-radius:6px; background:rgba(103,232,249,0.1); color:#67e8f9; font-size:0.78rem; cursor:pointer; white-space:nowrap; }
.unlock-all:disabled { opacity:0.45; cursor:not-allowed; }

.toolbar { display:flex; align-items:center; gap:10px; }
.tabs { display:flex; gap:6px; }
.tab { padding:6px 12px; border-radius:6px; border:1px solid rgba(255,255,255,0.1); background:transparent; color:rgba(255,255,255,0.4); font-size:0.75rem; font-family:inherit; cursor:pointer; transition:all 0.15s; white-space:nowrap; }
.tab:hover { color:rgba(255,255,255,0.7); border-color:rgba(103,232,249,0.25); }
.tab.on { color:#67e8f9; border-color:rgba(103,232,249,0.4); background:rgba(103,232,249,0.1); }
.search { flex:1; min-width:0; padding:7px 12px; border:1px solid rgba(255,255,255,0.14); border-radius:6px; background:rgba(255,255,255,0.06); color:#fff; font-size:0.8rem; font-family:inherit; }
.search::placeholder { color:rgba(255,255,255,0.28); }

.list { border-radius:12px; border:1px solid rgba(255,255,255,0.06); background:rgba(255,255,255,0.02); overflow-y:auto; overflow-x:hidden; scrollbar-width:thin; scrollbar-color:rgba(255,255,255,0.08) transparent; }
.empty { text-align:center; color:rgba(255,255,255,0.3); font-size:0.8rem; padding:24px; }
.spacer { position:relative; width:100%; }
.rows { position:absolute; top:0; left:0; right:0; }
.row { display:flex; align-items:center; gap:10px; padding:0 14px; border-bottom:1px solid rgba(255,255,255,0.03); box-sizing:border-box; }
.row:hover { background:rgba(255,255,255,0.025); }
.id { width:44px; font-size:0.68rem; color:rgba(255,255,255,0.2); font-family:'Courier New',monospace; flex-shrink:0; }
.name { flex:1; font-size:0.82rem; color:rgba(255,255,255,0.6); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.row.unlocked .name { color:rgba(255,255,255,0.85); }
.badge-state { width:52px; text-align:center; font-size:0.68rem; flex-shrink:0; color:rgba(255,255,255,0.28); }
.badge-state.on { color:#4ade80; }
.toggle { width:52px; flex-shrink:0; padding:5px 0; border-radius:5px; font-size:0.72rem; font-family:inherit; cursor:pointer; border:1px solid rgba(251,191,36,0.35); background:rgba(251,191,36,0.1); color:#fbbf24; transition:all 0.15s; }
.toggle.locked { border-color:rgba(103,232,249,0.35); background:rgba(103,232,249,0.1); color:#67e8f9; }
.toggle:disabled { opacity:0.4; cursor:not-allowed; }

.confirm-backdrop { position:fixed; inset:0; z-index:10; display:flex; align-items:center; justify-content:center; padding:20px; background:rgba(0,0,0,0.62); }
.confirm-dialog { width:min(440px,100%); padding:18px; border:1px solid rgba(103,232,249,0.24); border-radius:8px; background:#17222f; box-shadow:0 18px 48px rgba(0,0,0,0.55); }
.confirm-dialog h2 { margin:0 0 10px; color:#e8f8fb; font-size:0.95rem; font-weight:600; }
.confirm-dialog p { margin:0; color:rgba(255,255,255,0.66); font-size:0.8rem; line-height:1.65; }
.confirm-actions { display:flex; justify-content:flex-end; gap:8px; margin-top:16px; }
.confirm-actions button { padding:7px 14px; border-radius:5px; font-size:0.78rem; font-family:inherit; cursor:pointer; }
.confirm-cancel { border:1px solid rgba(255,255,255,0.15); background:transparent; color:rgba(255,255,255,0.62); }
.confirm-apply { border:1px solid rgba(103,232,249,0.35); background:rgba(103,232,249,0.14); color:#67e8f9; }
</style>
