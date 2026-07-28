<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { translate as t } from '../i18n'
import { matchText } from '../utils/matchText.js'

const props = defineProps({
  modelValue: { type: Number, default: 0 },
  options: { type: Array, default: () => [] },
  optional: { type: Boolean, default: false },
  placeholder: { type: String, default: () => t('picker.noSelection') },
  disabled: { type: Boolean, default: false },
})

const emit = defineEmits(['update:modelValue', 'pick', 'open', 'close'])

const open = ref(false)
const query = ref('')
const highlight = ref(0)
const rootEl = ref(null)
const searchEl = ref(null)
const listEl = ref(null)

function hex(v) { return '0x' + (Number(v) >>> 0).toString(16).toUpperCase().padStart(8, '0') }

const selected = computed(() => props.options.find(o => (o.hash >>> 0) === (props.modelValue >>> 0)) || null)

const filtered = computed(() => {
  const q = query.value.trim()
  if (!q) return props.options
  return props.options.filter(o => matchText(o.displayName, q) || matchText(hex(o.hash), q))
})

function openDropdown() {
  if (props.disabled) return
  open.value = true
  query.value = ''
  const idx = filtered.value.findIndex(o => (o.hash >>> 0) === (props.modelValue >>> 0))
  highlight.value = idx >= 0 ? idx : 0
  emit('open')
  nextTick(() => searchEl.value?.focus())
}

function closeDropdown() {
  if (!open.value) return
  open.value = false
  emit('close')
}

function commit(opt) {
  if (!opt) return
  emit('update:modelValue', opt.hash >>> 0)
  emit('pick', opt)
  closeDropdown()
}

function clearSelection() {
  emit('update:modelValue', 0)
  emit('pick', null)
  closeDropdown()
}

function onKey(e) {
  const list = filtered.value
  if (e.key === 'ArrowDown') { e.preventDefault(); highlight.value = Math.min(highlight.value + 1, list.length - 1); scrollToHighlight() }
  else if (e.key === 'ArrowUp') { e.preventDefault(); highlight.value = Math.max(highlight.value - 1, 0); scrollToHighlight() }
  else if (e.key === 'Enter') { e.preventDefault(); commit(list[highlight.value]) }
  else if (e.key === 'Escape') { e.preventDefault(); closeDropdown() }
}

function scrollToHighlight() {
  nextTick(() => {
    const el = listEl.value?.querySelector('.opt.hi')
    el?.scrollIntoView({ block: 'nearest' })
  })
}

watch(query, () => { highlight.value = 0 })

function onDocClick(e) {
  if (!open.value) return
  if (rootEl.value && !rootEl.value.contains(e.target)) closeDropdown()
}

watch(open, (v) => {
  if (v) document.addEventListener('mousedown', onDocClick)
  else document.removeEventListener('mousedown', onDocClick)
})

onBeforeUnmount(() => document.removeEventListener('mousedown', onDocClick))
</script>

<template>
  <div class="picker" :class="{ 'picker-open': open, disabled }" ref="rootEl">
    <button type="button" class="picker-selected" @click="open ? closeDropdown() : openDropdown()" :disabled="disabled">
      <span v-if="selected" class="picker-label">{{ selected.displayName }}</span>
      <span v-else class="picker-placeholder">{{ placeholder }}</span>
      <button v-if="optional && selected" type="button" class="picker-inline-clear" @click.stop="clearSelection" :title="t('picker.remove')">✕</button>
      <span class="cheveron">{{ open ? '▴' : '▾' }}</span>
    </button>
    <div v-if="open" class="picker-dropdown">
      <div class="picker-search">
        <input ref="searchEl" v-model="query" @keydown="onKey" :placeholder="t('picker.searchPlaceholder')" />
      </div>
      <div class="picker-list" ref="listEl">
        <div v-if="!filtered.length" class="picker-none">{{ t('picker.noMatch') }}</div>
        <div
          v-for="(opt, i) in filtered"
          :key="opt.hash"
          class="opt"
          :class="{ hi: i === highlight, selected: (opt.hash >>> 0) === (modelValue >>> 0) }"
          @mousedown.prevent="commit(opt)"
          @mouseenter="highlight = i"
          :title="hex(opt.hash)"
        >
          <span class="opt-name">
            {{ opt.displayName }}
            <span v-if="opt.source === 'memory-only'" class="opt-tag">{{ t('picker.supplement') }}</span>
          </span>
          <span v-if="opt.maxLevel != null" class="opt-max">{{ t('sigil.common.level', { level: opt.maxLevel }) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.picker { position: relative; flex: 1; font-family:inherit; }
.picker-selected { display:flex; align-items:center; gap:6px; width:100%; padding:8px 10px; border:1px solid rgba(255,255,255,.12); border-radius:6px; background:rgba(255,255,255,.06); color:#fff; cursor:pointer; font-family:inherit; font-size:.8rem; text-align:left; }
.picker-selected:hover { border-color:rgba(103,232,249,.35); }
.picker-selected:disabled { opacity:.45; cursor:not-allowed; }
.picker-open .picker-selected { border-color:rgba(103,232,249,.5); border-bottom-left-radius:0; border-bottom-right-radius:0; }
.picker-label, .picker-placeholder { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.picker-placeholder { color:rgba(255,255,255,.4); }
.picker-inline-clear { flex-shrink:0; background:transparent; border:0; padding:2px 6px; color:rgba(255,255,255,.4); font:inherit; font-size:.8rem; line-height:1; cursor:pointer; border-radius:3px; font-family:inherit; }
.picker-inline-clear:hover { color:#f87171; background:rgba(248,113,113,.12); }
.cheveron { color:rgba(255,255,255,.35); font-size:.7rem; flex-shrink:0; }
.picker-dropdown { position:absolute; top:100%; left:0; right:0; z-index:20; background:#1b2636; border:1px solid rgba(103,232,249,.5); border-top:none; border-radius:0 0 6px 6px; box-shadow:0 6px 16px rgba(0,0,0,.35); }
.picker-search { display:flex; align-items:center; gap:6px; padding:6px 10px; border-bottom:1px solid rgba(255,255,255,.06); }
.picker-search input { flex:1; background:transparent; border:none; color:#fff; outline:none; font-family:inherit; font-size:.78rem; padding:2px 0; }
.clear-btn { background:transparent; border:1px solid rgba(255,255,255,.15); border-radius:4px; padding:2px 8px; font-size:.68rem; color:rgba(255,255,255,.55); cursor:pointer; font-family:inherit; }
.clear-btn:hover { color:#67e8f9; border-color:rgba(103,232,249,.35); }
.picker-list { max-height:220px; overflow-y:auto; padding:4px 0; }
.opt { display:flex; justify-content:space-between; align-items:center; gap:12px; padding:6px 12px; font-size:.78rem; color:rgba(255,255,255,.72); cursor:pointer; font-family:inherit; }
.opt:hover, .opt.hi { background:rgba(103,232,249,.12); color:#fff; }
.opt.selected::before { content:"✓ "; color:#67e8f9; }
.opt-name { display:inline-flex; align-items:center; gap:6px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.opt-tag { padding:1px 5px; border:1px solid rgba(103,232,249,.35); border-radius:3px; color:#67e8f9; font-size:.6rem; letter-spacing:.5px; font-family:inherit; }
.opt-max { color:rgba(255,255,255,.4); font-size:.7rem; font-weight:600; flex-shrink:0; }
.picker-none { padding:14px; text-align:center; color:rgba(255,255,255,.35); font-size:.72rem; }
</style>
