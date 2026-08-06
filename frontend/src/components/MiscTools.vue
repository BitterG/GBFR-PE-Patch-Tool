<script setup>
import { translate as t } from '../i18n'
import { onBeforeUnmount, reactive, ref } from 'vue'
import { CharaAttach, CharaDetach,
         CurrencyGetAll, CurrencySetOne,
         PotionGetAll, PotionSetOne,
         CountdownGetStatus, CountdownScan, CountdownSet,
         FaceAccessoryGetStatus, FaceAccessoryScan, FaceAccessorySetHidden,
         InfiniteChallengeGetStatus, InfiniteChallengeSetEnabled,
         MaterialConsumeGetStatus, MaterialConsumeSetEnabled,
         CollectibleTaskComplete,
         MonsterEnhanceSetPatchValueEnabled,
         TerminusDropGetStatus, TerminusDropScan, TerminusDropSetEnabled,
         UnlockAllTrophyGetStatus, UnlockAllTrophyScan, UnlockAllTrophySetEnabled,
         OtherSkinPurpleRuneGetStatus, OtherSkinPurpleRuneSetEnabled,
         DamageMeterGetStatus, DamageMeterReset,
         DamageOverlaySetEnabled, DamageOverlaySetValue, DamageOverlaySetFontSize,
         PlayerPositionGet, PlayerPositionSet,
         FlightGetStatus, FlightSetEnabled,
         GetAppVersion, CheckUpdate, OpenReleasePage } from '../../wailsjs/go/main/App'

const emit = defineEmits(['status'])

const connected = ref(false)
const info = reactive({ pid: 0, moduleBase: 0, manager: 0 })
const loading = ref(false)

const countdownValue = ref('30')
const countdownStatus = reactive({ found: false, address: 0, rva: 0, value1: 0, value2: 0, currentBytes: '' })
const countdownLoading = ref(false)
const faceAccessoryStatus = reactive({ found: false, address: 0, rva: 0, hidden: false, jumpOpcode: '', currentBytes: '' })
const faceAccessoryLoading = ref(false)
const infiniteChallengeStatus = reactive({ rva: 0, enabled: false, currentBytes: '' })
const infiniteChallengeLoading = ref(false)
const materialConsumeStatus = reactive({ rva: 0, enabled: false, currentBytes: '' })
const materialConsumeLoading = ref(false)
const collectibleTaskLoading = ref(false)
const inventorySet45Enabled = ref(false)
const inventorySet45Loading = ref(false)
const inventorySet45Seconds = ref(0)
const inventorySetQuantity = ref(45)
const terminusDropStatus = reactive({ found: false, address: 0, rva: 0, enabled: false, currentBytes: '' })
const terminusDropLoading = ref(false)
const unlockAllTrophyStatus = reactive({ found: false, address: 0, rva: 0, enabled: false, currentBytes: '' })
const unlockAllTrophyLoading = ref(false)
const showUnlockAllTrophyConfirm = ref(false)
const otherSkinPurpleRuneStatus = reactive({ rva: 0, enabled: false, jumpOpcode: '', currentBytes: '' })
const otherSkinPurpleRuneLoading = ref(false)
const updateInfo = reactive({ currentVersion: 'v1.5.0', latestVersion: '', hasUpdate: false, releaseUrl: '', body: '', assets: [] })
const updateLoading = ref(false)
const damageMeterStatus = reactive({ connected: false, totalDamage: 0, monsterDamage: 0 })
const damageMeterLoading = ref(false)
const playerPosition = reactive({ x: 0, y: 0, z: 0, address: 0 })
const playerPositionInput = reactive({ x: '', y: '', z: '' })
const playerPositionLoading = ref(false)
const playerPositionLoaded = ref(false)
const flightStatus = reactive({ enabled: false, speed: 8 })
const flightLoading = ref(false)
const currencies = ref([])
const currencyInputs = reactive({})
const currencyLoading = ref(false)
const potions = ref([])
const potionInputs = reactive({})
const potionLoading = ref(false)
const damageOverlayEnabled = ref(false)
const damageOverlayFontSize = ref(Number(localStorage.getItem('gbfrDamageOverlayFontSize') || 48))
const showOutdatedFeatures = false
let damageMeterTimer = 0
let inventorySet45Timer = 0

function getMonsterEnhanceMultiplier(id) {
  const saved = window.gbfrMonsterEnhanceMultipliers || {}
  const value = parseFloat(saved[id] || '1')
  return isNaN(value) || value <= 0 || value > 9999 ? 1 : value
}

GetAppVersion().then(v => { updateInfo.currentVersion = v }).catch(() => {})

function connect() {
  loading.value = true
  CharaAttach()
    .then((res) => {
      connected.value = true
      Object.assign(info, res)
      if (showOutdatedFeatures) {
        loadCountdownStatus()
        loadFaceAccessoryStatus()
      }
      loadInfiniteChallengeStatus()
      loadMaterialConsumeStatus()
      if (showOutdatedFeatures) {
        loadTerminusDropStatus()
        loadUnlockAllTrophyStatus()
        loadOtherSkinPurpleRuneStatus()
      }
      loadCurrencyValues()
      loadPotionValues()
      if (showOutdatedFeatures) startDamageMeterTimer()
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { loading.value = false })
}

function disconnect() {
  CharaDetach()
    .then(() => {
      connected.value = false
      stopDamageMeterTimer()
      Object.assign(info, { pid: 0, moduleBase: 0, manager: 0 })
      Object.assign(countdownStatus, { found: false, address: 0, rva: 0, value1: 0, value2: 0, currentBytes: '' })
      Object.assign(faceAccessoryStatus, { found: false, address: 0, rva: 0, hidden: false, jumpOpcode: '', currentBytes: '' })
      Object.assign(infiniteChallengeStatus, { rva: 0, enabled: false, currentBytes: '' })
      Object.assign(materialConsumeStatus, { rva: 0, enabled: false, currentBytes: '' })
      Object.assign(terminusDropStatus, { found: false, address: 0, rva: 0, enabled: false, currentBytes: '' })
      Object.assign(unlockAllTrophyStatus, { found: false, address: 0, rva: 0, enabled: false, currentBytes: '' })
      Object.assign(otherSkinPurpleRuneStatus, { rva: 0, enabled: false, jumpOpcode: '', currentBytes: '' })
      Object.assign(damageMeterStatus, { connected: false, totalDamage: 0, monsterDamage: 0 })
      Object.assign(playerPosition, { x: 0, y: 0, z: 0, address: 0 })
      playerPositionLoaded.value = false
      Object.assign(flightStatus, { enabled: false, speed: 8 })
      currencies.value = []
      Object.keys(currencyInputs).forEach((key) => delete currencyInputs[key])
      potions.value = []
      Object.keys(potionInputs).forEach((key) => delete potionInputs[key])
    })
    .catch((err) => emit('status', String(err), 'error'))
}

function formatHex(value) {
  if (!value) return '—'
  return '0x' + Number(value).toString(16).toUpperCase()
}

function formatFloat(value) {
  if (value === undefined || value === null) return '—'
  return Number(value).toFixed(2)
}

function isCountdownActive() {
  return countdownStatus.found && Math.abs(Number(countdownStatus.value1) - 30) > 0.001
}

function applyCountdownStatus(status) {
  Object.assign(countdownStatus, status || { found: false, address: 0, rva: 0, value1: 0, value2: 0, currentBytes: '' })
  if (status && status.found) countdownValue.value = String(Number(status.value1.toFixed(2)))
}

function loadCountdownStatus() {
  if (!connected.value) return
  countdownLoading.value = true
  CountdownGetStatus()
    .then(applyCountdownStatus)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { countdownLoading.value = false })
}

function scanCountdown() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  countdownLoading.value = true
  CountdownScan()
    .then((status) => { applyCountdownStatus(status); emit('status', t('runtimeTools.misc.countdown.scanSuccess'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { countdownLoading.value = false })
}

function setCountdown() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  const v = parseFloat(countdownValue.value)
  if (isNaN(v) || v < 0 || v > 9999) { emit('status', t('runtimeTools.messages.numberRange', { min: 0, max: 9999 }), 'error'); return }
  countdownLoading.value = true
  CountdownSet(v)
    .then((status) => { applyCountdownStatus(status); emit('status', t('runtimeTools.misc.countdown.setSuccess'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { countdownLoading.value = false })
}

function applyFaceAccessoryStatus(status) {
  Object.assign(faceAccessoryStatus, status || { found: false, address: 0, rva: 0, hidden: false, jumpOpcode: '', currentBytes: '' })
}

function loadFaceAccessoryStatus() {
  if (!connected.value) return
  faceAccessoryLoading.value = true
  FaceAccessoryGetStatus()
    .then(applyFaceAccessoryStatus)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { faceAccessoryLoading.value = false })
}

function scanFaceAccessory() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  faceAccessoryLoading.value = true
  FaceAccessoryScan()
    .then((status) => { applyFaceAccessoryStatus(status); emit('status', t('runtimeTools.misc.face.scanSuccess'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { faceAccessoryLoading.value = false })
}

function setFaceAccessoryHidden(hidden) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  faceAccessoryLoading.value = true
  FaceAccessorySetHidden(hidden)
    .then((status) => { applyFaceAccessoryStatus(status); emit('status', hidden ? t('runtimeTools.misc.face.hiddenSuccess') : t('runtimeTools.misc.face.visibleSuccess'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { faceAccessoryLoading.value = false })
}

function applyInfiniteChallengeStatus(status) {
  Object.assign(infiniteChallengeStatus, status || { rva: 0, enabled: false, currentBytes: '' })
}

function loadInfiniteChallengeStatus() {
  if (!connected.value) return
  infiniteChallengeLoading.value = true
  InfiniteChallengeGetStatus()
    .then(applyInfiniteChallengeStatus)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { infiniteChallengeLoading.value = false })
}

function setInfiniteChallengeEnabled(enabled) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  infiniteChallengeLoading.value = true
  InfiniteChallengeSetEnabled(enabled)
    .then((status) => { applyInfiniteChallengeStatus(status); emit('status', enabled ? t('runtimeTools.misc.challenge.enabled') : t('runtimeTools.misc.challenge.disabled'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { infiniteChallengeLoading.value = false })
}

function applyMaterialConsumeStatus(status) {
  Object.assign(materialConsumeStatus, status || { rva: 0, enabled: false, currentBytes: '' })
}

function loadMaterialConsumeStatus() {
  if (!connected.value) return
  materialConsumeLoading.value = true
  MaterialConsumeGetStatus()
    .then(applyMaterialConsumeStatus)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { materialConsumeLoading.value = false })
}

function setMaterialConsumeEnabled(enabled) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  materialConsumeLoading.value = true
  MaterialConsumeSetEnabled(enabled)
    .then((status) => { applyMaterialConsumeStatus(status); emit('status', enabled ? t('runtimeTools.misc.material.enabled') : t('runtimeTools.misc.material.disabled'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { materialConsumeLoading.value = false })
}

function completeCollectibleTask() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  collectibleTaskLoading.value = true
  CollectibleTaskComplete()
    .then((status) => emit('status', t('runtimeTools.misc.crab.completed', status), 'success'))
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { collectibleTaskLoading.value = false })
}

function stopInventorySet45Timer() {
  if (inventorySet45Timer) window.clearInterval(inventorySet45Timer)
  inventorySet45Timer = 0
  inventorySet45Seconds.value = 0
}

function startInventorySet45Timer() {
  stopInventorySet45Timer()
  inventorySet45Seconds.value = 10
  inventorySet45Timer = window.setInterval(() => {
    inventorySet45Seconds.value -= 1
    if (inventorySet45Seconds.value > 0) return
    stopInventorySet45Timer()
    setInventorySet45Enabled(false, 0, true)
  }, 1000)
}

function setInventorySet45Enabled(enabled, quantity = inventorySetQuantity.value, automatic = false) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  if (!enabled) stopInventorySet45Timer()
  inventorySet45Loading.value = true
  MonsterEnhanceSetPatchValueEnabled('inventory_set_45', enabled, quantity)
    .then(() => {
      inventorySet45Enabled.value = enabled
      if (enabled) {
        inventorySetQuantity.value = quantity
        startInventorySet45Timer()
      }
      emit('status', enabled ? t('runtimeTools.misc.crab.enabled', { quantity }) : (automatic ? t('runtimeTools.misc.crab.automaticRestore') : t('runtimeTools.misc.crab.restoreSuccess')), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { inventorySet45Loading.value = false })
}

function applyTerminusDropStatus(status) {
  Object.assign(terminusDropStatus, status || { found: false, address: 0, rva: 0, enabled: false, currentBytes: '' })
}

function loadTerminusDropStatus() {
  if (!connected.value) return
  terminusDropLoading.value = true
  TerminusDropGetStatus()
    .then(applyTerminusDropStatus)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { terminusDropLoading.value = false })
}

function scanTerminusDrop() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  terminusDropLoading.value = true
  TerminusDropScan()
    .then((status) => { applyTerminusDropStatus(status); emit('status', t('runtimeTools.misc.terminus.scanSuccess'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { terminusDropLoading.value = false })
}

function setTerminusDropEnabled(enabled) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  terminusDropLoading.value = true
  TerminusDropSetEnabled(enabled)
    .then((status) => { applyTerminusDropStatus(status); emit('status', enabled ? t('runtimeTools.misc.terminus.enabled') : t('runtimeTools.misc.terminus.disabled'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { terminusDropLoading.value = false })
}

function applyUnlockAllTrophyStatus(status) {
  Object.assign(unlockAllTrophyStatus, status || { found: false, address: 0, rva: 0, enabled: false, currentBytes: '' })
}

function loadUnlockAllTrophyStatus() {
  if (!connected.value) return
  unlockAllTrophyLoading.value = true
  UnlockAllTrophyGetStatus()
    .then(applyUnlockAllTrophyStatus)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { unlockAllTrophyLoading.value = false })
}

function scanUnlockAllTrophy() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  unlockAllTrophyLoading.value = true
  UnlockAllTrophyScan()
    .then((status) => { applyUnlockAllTrophyStatus(status); emit('status', t('runtimeTools.misc.trophy.scanSuccess'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { unlockAllTrophyLoading.value = false })
}

function setUnlockAllTrophyEnabled(enabled) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  if (enabled) { showUnlockAllTrophyConfirm.value = true; return }
  applyUnlockAllTrophyEnabled(false)
}

function confirmUnlockAllTrophy() {
  showUnlockAllTrophyConfirm.value = false
  applyUnlockAllTrophyEnabled(true)
}

function applyUnlockAllTrophyEnabled(enabled) {
  unlockAllTrophyLoading.value = true
  UnlockAllTrophySetEnabled(enabled)
    .then((status) => { applyUnlockAllTrophyStatus(status); emit('status', enabled ? t('runtimeTools.misc.trophy.enabled') : t('runtimeTools.misc.trophy.disabled'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { unlockAllTrophyLoading.value = false })
}

function applyOtherSkinPurpleRuneStatus(status) {
  Object.assign(otherSkinPurpleRuneStatus, status || { rva: 0, enabled: false, jumpOpcode: '', currentBytes: '' })
}

function loadOtherSkinPurpleRuneStatus() {
  if (!connected.value) return
  otherSkinPurpleRuneLoading.value = true
  OtherSkinPurpleRuneGetStatus()
    .then(applyOtherSkinPurpleRuneStatus)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { otherSkinPurpleRuneLoading.value = false })
}

function setOtherSkinPurpleRuneEnabled(enabled) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  otherSkinPurpleRuneLoading.value = true
  OtherSkinPurpleRuneSetEnabled(enabled)
    .then((status) => { applyOtherSkinPurpleRuneStatus(status); emit('status', enabled ? t('runtimeTools.misc.purpleRune.enabled') : t('runtimeTools.misc.purpleRune.disabled'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { otherSkinPurpleRuneLoading.value = false })
}

function formatDamage(value) {
  return Number(value || 0).toLocaleString()
}

function formatInt(value) {
  return Number(value || 0).toLocaleString()
}

function currencyName(item) {
  return t(`runtimeTools.misc.currency.items.${item.id}`)
}

function potionName(item) {
  return t(`runtimeTools.misc.potion.items.${item.id}`)
}

function applyCurrencyValues(items) {
  currencies.value = Array.isArray(items) ? items : []
  currencies.value.forEach((item) => {
    currencyInputs[item.id] = String(item.value)
  })
}

function loadCurrencyValues() {
  if (!connected.value) return
  currencyLoading.value = true
  CurrencyGetAll()
    .then(applyCurrencyValues)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { currencyLoading.value = false })
}

function setCurrency(item) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  const value = Number(currencyInputs[item.id])
  if (!Number.isInteger(value) || value < 0 || value > 2147483647) { emit('status', t('runtimeTools.messages.integerRange', { min: 0, max: 2147483647 }), 'error'); return }
  currencyLoading.value = true
  CurrencySetOne(item.id, value)
    .then((updated) => {
      const index = currencies.value.findIndex((entry) => entry.id === updated.id)
      if (index >= 0) currencies.value.splice(index, 1, updated)
      currencyInputs[updated.id] = String(updated.value)
      emit('status', t('runtimeTools.messages.writeSuccess', { name: currencyName(updated) }), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { currencyLoading.value = false })
}

function formatOffsets(offsets) {
  return (offsets || []).map(formatHex).join(' + ')
}

function applyPotionValues(items) {
  potions.value = Array.isArray(items) ? items : []
  potions.value.forEach((item) => {
    potionInputs[item.id] = String(item.value)
  })
}

function loadPotionValues() {
  if (!connected.value) return
  potionLoading.value = true
  PotionGetAll()
    .then(applyPotionValues)
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { potionLoading.value = false })
}

function setPotion(item) {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  const value = Number(potionInputs[item.id])
  if (!Number.isInteger(value) || value < 0 || value > 2147483647) { emit('status', t('runtimeTools.messages.integerRange', { min: 0, max: 2147483647 }), 'error'); return }
  potionLoading.value = true
  PotionSetOne(item.id, value)
    .then((updated) => {
      const index = potions.value.findIndex((entry) => entry.id === updated.id)
      if (index >= 0) potions.value.splice(index, 1, updated)
      potionInputs[updated.id] = String(updated.value)
      emit('status', t('runtimeTools.messages.writeSuccess', { name: potionName(updated) }), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { potionLoading.value = false })
}

function loadPlayerPosition() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  playerPositionLoading.value = true
  PlayerPositionGet()
    .then((position) => {
      Object.assign(playerPosition, position)
      Object.assign(playerPositionInput, { x: String(position.x), y: String(position.y), z: String(position.z) })
      playerPositionLoaded.value = true
    })
    .catch((err) => {
      playerPositionLoaded.value = false
      emit('status', String(err), 'error')
    })
    .finally(() => { playerPositionLoading.value = false })
}

function setPlayerPosition() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  const x = Number(playerPositionInput.x)
  const y = Number(playerPositionInput.y)
  const z = Number(playerPositionInput.z)
  if (![x, y, z].every(Number.isFinite) || [x, y, z].some((value) => Math.abs(value) > 10000000)) {
    emit('status', t('runtimeTools.misc.position.invalid'), 'error')
    return
  }
  playerPositionLoading.value = true
  PlayerPositionSet(x, y, z)
    .then((position) => {
      Object.assign(playerPosition, position)
      Object.assign(playerPositionInput, { x: String(position.x), y: String(position.y), z: String(position.z) })
      playerPositionLoaded.value = true
      emit('status', t('runtimeTools.misc.position.setSuccess'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { playerPositionLoading.value = false })
}

function toggleFlight() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  flightLoading.value = true
  const enabled = !flightStatus.enabled
  const speed = Number(flightStatus.speed)
  FlightSetEnabled(enabled, speed)
    .then((status) => {
      Object.assign(flightStatus, status)
      emit('status', enabled ? t('runtimeTools.misc.flight.enabled') : t('runtimeTools.misc.flight.disabled'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { flightLoading.value = false })
}

function applyDamageMeterStatus(status) {
  Object.assign(damageMeterStatus, {
    connected: !!(status && status.connected),
    totalDamage: Number((status && status.totalDamage) || 0),
    monsterDamage: Number((status && status.monsterDamage) || 0),
  })
  if (damageOverlayEnabled.value) DamageOverlaySetValue(displayDamage()).catch(() => {})
}

function displayDamage() {
  return Math.round(damageMeterStatus.monsterDamage * getMonsterEnhanceMultiplier('monster_hp'))
}

function startDamageMeterTimer() {
  stopDamageMeterTimer()
  loadDamageMeterStatus()
  damageMeterTimer = window.setInterval(() => loadDamageMeterStatus(true), 500)
}

function stopDamageMeterTimer() {
  if (!damageMeterTimer) return
  window.clearInterval(damageMeterTimer)
  damageMeterTimer = 0
}

function loadDamageMeterStatus(silent = false) {
  if (!connected.value) return
  if (!silent) damageMeterLoading.value = true
  DamageMeterGetStatus()
    .then(applyDamageMeterStatus)
    .catch((err) => { if (!silent) emit('status', String(err), 'error') })
    .finally(() => { if (!silent) damageMeterLoading.value = false })
}

function enableDamageMeter() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  damageMeterLoading.value = true
  MonsterEnhanceSetPatchValueEnabled('monster_hp', true, getMonsterEnhanceMultiplier('monster_hp'))
    .then(() => DamageMeterGetStatus())
    .then((status) => {
      applyDamageMeterStatus(status)
      emit('status', t('runtimeTools.misc.damage.enabled'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { damageMeterLoading.value = false })
}

function resetDamageMeter() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  damageMeterLoading.value = true
  DamageMeterReset()
    .then((status) => { applyDamageMeterStatus(status); emit('status', t('runtimeTools.misc.damage.resetSuccess'), 'success') })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { damageMeterLoading.value = false })
}

function clampOverlayFontSize(value) {
  return Math.min(120, Math.max(18, Number(value) || 48))
}

function setDamageOverlayFontSize(value) {
  damageOverlayFontSize.value = clampOverlayFontSize(value)
  localStorage.setItem('gbfrDamageOverlayFontSize', String(damageOverlayFontSize.value))
  DamageOverlaySetFontSize(damageOverlayFontSize.value).catch(() => {})
}

function enableDamageOverlay() {
  if (!connected.value) { emit('status', t('runtimeTools.messages.connectFirst'), 'error'); return }
  DamageOverlaySetFontSize(damageOverlayFontSize.value)
    .then(() => DamageOverlaySetValue(displayDamage()))
    .then(() => DamageOverlaySetEnabled(true))
    .then(() => {
      damageOverlayEnabled.value = true
      startDamageMeterTimer()
      emit('status', t('runtimeTools.misc.damage.overlayEnabled'), 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
}

function disableDamageOverlay() {
  DamageOverlaySetEnabled(false).catch(() => {})
  damageOverlayEnabled.value = false
  emit('status', t('runtimeTools.misc.damage.overlayDisabled'), 'success')
}

function toggleDamageOverlay() {
  if (damageOverlayEnabled.value) disableDamageOverlay()
  else enableDamageOverlay()
}

function checkUpdate() {
  updateLoading.value = true
  CheckUpdate()
    .then((info) => {
      Object.assign(updateInfo, info)
      emit('status', info.hasUpdate ? t('runtimeTools.misc.updates.found', { version: info.latestVersion }) : t('runtimeTools.misc.updates.alreadyLatest'), info.hasUpdate ? 'success' : 'success')
    })
    .catch((err) => emit('status', String(err), 'error'))
    .finally(() => { updateLoading.value = false })
}

function openReleasePage() {
  OpenReleasePage(updateInfo.releaseUrl || '')
    .catch((err) => emit('status', String(err), 'error'))
}

onBeforeUnmount(() => {
  stopDamageMeterTimer()
  stopInventorySet45Timer()
})

</script>

<template>
  <div class="root">
    <div class="section">
      <div class="header">
        <span class="title">{{ t('runtimeTools.misc.title') }}</span>
        <span class="info-dot" :title="t('runtimeTools.misc.runtimeNotice')">!</span>
        <span class="hint">{{ t('runtimeTools.misc.runtimeHint') }}</span>
      </div>
      <div class="connect-row">
        <button v-if="!connected" class="btn-connect" @click="connect" :disabled="loading">
          {{ loading ? t('runtimeTools.common.connecting') : t('runtimeTools.common.connectGame') }}
        </button>
        <button v-else class="btn-disconnect" @click="disconnect">{{ t('runtimeTools.common.disconnect') }}</button>
        <span v-if="connected" class="pid">{{ t('runtimeTools.common.pid') }}: {{ info.pid }}</span>
      </div>

      <div class="memory-card">
        <div class="memory-header">
          <span class="memory-title">{{ t('runtimeTools.misc.updates.title') }}</span>
          <span class="memory-hint">{{ t('runtimeTools.misc.updates.current', { version: updateInfo.currentVersion }) }}</span>
        </div>
        <div class="memory-info">
          <span>{{ t('runtimeTools.misc.updates.latest', { version: updateInfo.latestVersion || t('runtimeTools.misc.updates.unchecked') }) }}</span>
          <span v-if="updateInfo.hasUpdate" class="update-new">{{ t('runtimeTools.misc.updates.available') }}</span>
          <span v-else-if="updateInfo.latestVersion">{{ t('runtimeTools.misc.updates.upToDate') }}</span>
        </div>
        <div v-if="updateInfo.body" class="update-body">{{ updateInfo.body }}</div>
        <div class="memory-row">
          <button class="btn-batch" @click="checkUpdate" :disabled="updateLoading">{{ updateLoading ? t('runtimeTools.misc.updates.checking') : t('runtimeTools.misc.updates.check') }}</button>
          <button class="btn-refresh" @click="openReleasePage">{{ t('runtimeTools.misc.updates.openRelease') }}</button>
        </div>
      </div>

      <template v-if="connected">
        <div class="memory-card" :class="{ active: currencies.length }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.currency.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.currency.hint') }}</span>
          </div>
          <div class="currency-grid">
            <div v-for="item in currencies" :key="item.id" class="currency-row">
              <div class="currency-name">{{ currencyName(item) }}</div>
              <div class="currency-meta">{{ formatInt(item.value) }} · {{ formatHex(item.rva) }} + {{ formatHex(item.offset) }}</div>
              <input v-model="currencyInputs[item.id]" type="number" min="0" max="2147483647" step="1" class="batch-input currency-input" />
              <button class="btn-batch" @click="setCurrency(item)" :disabled="currencyLoading">{{ t('runtimeTools.common.write') }}</button>
            </div>
          </div>
          <div class="memory-row">
            <button class="btn-refresh" @click="loadCurrencyValues" :disabled="currencyLoading">{{ t('runtimeTools.misc.currency.refresh') }}</button>
          </div>
        </div>

        <div class="memory-card" :class="{ active: potions.length }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.potion.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.potion.hint') }}</span>
          </div>
          <div class="currency-grid">
            <div v-for="item in potions" :key="item.id" class="currency-row">
              <div class="currency-name">{{ potionName(item) }}</div>
              <div class="currency-meta">{{ formatInt(item.value) }} · {{ formatHex(item.rva) }} + {{ formatOffsets(item.offsets) }}</div>
              <input v-model="potionInputs[item.id]" type="number" min="0" max="2147483647" step="1" class="batch-input currency-input" />
              <button class="btn-batch" @click="setPotion(item)" :disabled="potionLoading">{{ t('runtimeTools.common.write') }}</button>
            </div>
          </div>
          <div class="memory-row">
            <button class="btn-refresh" @click="loadPotionValues" :disabled="potionLoading">{{ t('runtimeTools.misc.potion.refresh') }}</button>
          </div>
        </div>

        <div class="memory-card" :class="{ active: playerPositionLoaded }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.position.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.position.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.misc.position.location', { x: formatFloat(playerPosition.x), y: formatFloat(playerPosition.y), z: formatFloat(playerPosition.z) }) }}</span>
            <span>{{ t('runtimeTools.misc.position.entity', { address: formatHex(playerPosition.address) }) }}</span>
          </div>
          <div class="memory-row">
            <input v-model="playerPositionInput.x" type="number" step="any" class="batch-input coordinate-input" :placeholder="t('runtimeTools.common.axisX')" />
            <input v-model="playerPositionInput.y" type="number" step="any" class="batch-input coordinate-input" :placeholder="t('runtimeTools.common.axisY')" />
            <input v-model="playerPositionInput.z" type="number" step="any" class="batch-input coordinate-input" :placeholder="t('runtimeTools.common.axisZ')" />
            <button class="btn-batch" @click="setPlayerPosition" :disabled="playerPositionLoading">{{ t('runtimeTools.misc.position.set') }}</button>
            <button class="btn-refresh" @click="loadPlayerPosition" :disabled="playerPositionLoading">{{ playerPositionLoading ? t('runtimeTools.misc.position.reading') : t('runtimeTools.misc.position.refresh') }}</button>
          </div>
        </div>

        <div class="memory-card" :class="{ active: flightStatus.enabled }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.flight.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.flight.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.status') }}: {{ flightStatus.enabled ? t('runtimeTools.common.enabled') : t('runtimeTools.common.disabled') }}</span>
            <span>{{ t('runtimeTools.misc.flight.speed', { speed: formatFloat(flightStatus.speed) }) }}</span>
          </div>
          <div class="memory-row">
            <input v-model.number="flightStatus.speed" type="number" min="0.1" max="1000" step="0.5" class="batch-input coordinate-input" :disabled="flightStatus.enabled" />
            <button class="btn-batch" @click="toggleFlight" :disabled="flightLoading">{{ flightStatus.enabled ? t('runtimeTools.misc.flight.disable') : t('runtimeTools.misc.flight.enable') }}</button>
          </div>
        </div>

        <div class="memory-card" :class="{ active: damageMeterStatus.connected && damageMeterStatus.totalDamage > 0 }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.damage.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.damage.hint') }}</span>
          </div>
          <div class="memory-info damage-meter-info">
            <span>{{ t('runtimeTools.common.status') }}: {{ damageMeterStatus.connected ? t('runtimeTools.misc.damage.recording') : t('runtimeTools.misc.damage.waiting') }}</span>
            <span>{{ t('runtimeTools.misc.damage.scaled') }}</span>
          </div>
          <div class="damage-meter-value">{{ formatDamage(displayDamage()) }}</div>
          <div class="damage-meter-raw">{{ t('runtimeTools.misc.damage.raw', { value: formatDamage(damageMeterStatus.totalDamage) }) }}</div>
          <div class="memory-row">
            <button class="btn-batch" @click="enableDamageMeter" :disabled="damageMeterLoading">{{ t('runtimeTools.misc.damage.start') }}</button>
            <button class="btn-refresh" @click="toggleDamageOverlay" :disabled="damageMeterLoading || !damageMeterStatus.connected">{{ damageOverlayEnabled ? t('runtimeTools.misc.damage.overlayOff') : t('runtimeTools.misc.damage.overlayOn') }}</button>
            <button class="btn-refresh" @click="loadDamageMeterStatus" :disabled="damageMeterLoading">{{ t('runtimeTools.common.refresh') }}</button>
            <button class="btn-refresh" @click="resetDamageMeter" :disabled="damageMeterLoading">{{ t('runtimeTools.misc.damage.reset') }}</button>
            <button class="btn-sort" @click="setDamageOverlayFontSize(damageOverlayFontSize - 4)" :disabled="!damageOverlayEnabled">{{ t('runtimeTools.misc.damage.fontDown') }}</button>
            <button class="btn-sort" @click="setDamageOverlayFontSize(damageOverlayFontSize + 4)" :disabled="!damageOverlayEnabled">{{ t('runtimeTools.misc.damage.fontUp') }}</button>
          </div>
        </div>

        <div v-if="showOutdatedFeatures" class="memory-card" :class="{ active: isCountdownActive() }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.countdown.title') }}</span>
            <span class="info-dot" :title="t('runtimeTools.misc.countdown.notice')">!</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.countdown.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(countdownStatus.rva) }}</span>
            <span>{{ t('runtimeTools.common.status') }}: {{ isCountdownActive() ? t('runtimeTools.common.enabled') : t('runtimeTools.common.default') }}</span>
            <span>{{ t('runtimeTools.misc.countdown.current', { first: formatFloat(countdownStatus.value1), second: formatFloat(countdownStatus.value2) }) }}</span>
          </div>
          <div class="memory-row">
            <input v-model="countdownValue" type="number" min="0" max="9999" step="0.1" class="batch-input countdown-input" :placeholder="t('runtimeTools.misc.countdown.placeholder')" />
            <button class="btn-batch" @click="setCountdown" :disabled="countdownLoading">{{ t('runtimeTools.misc.countdown.set') }}</button>
            <button class="btn-refresh" @click="loadCountdownStatus" :disabled="countdownLoading">{{ t('runtimeTools.common.refresh') }}</button>
            <button class="btn-sort" @click="scanCountdown" :disabled="countdownLoading">{{ t('runtimeTools.common.rescan') }}</button>
          </div>
          <div class="memory-bytes">{{ countdownStatus.currentBytes || t('runtimeTools.common.notLocated') }}</div>
        </div>

        <div v-if="showOutdatedFeatures" class="memory-card" :class="{ active: faceAccessoryStatus.hidden }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.face.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.face.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(faceAccessoryStatus.rva) }}</span>
            <span>{{ t('runtimeTools.common.status') }}: {{ faceAccessoryStatus.hidden ? t('runtimeTools.misc.face.hidden') : t('runtimeTools.misc.face.visible') }}</span>
            <span>{{ t('runtimeTools.misc.face.jump', { opcode: faceAccessoryStatus.jumpOpcode || '—' }) }}</span>
          </div>
          <div class="memory-row">
            <button class="btn-batch" @click="setFaceAccessoryHidden(true)" :disabled="faceAccessoryLoading || faceAccessoryStatus.hidden">{{ t('runtimeTools.misc.face.hide') }}</button>
            <button class="btn-refresh" @click="setFaceAccessoryHidden(false)" :disabled="faceAccessoryLoading || !faceAccessoryStatus.hidden">{{ t('runtimeTools.misc.face.restore') }}</button>
            <button class="btn-refresh" @click="loadFaceAccessoryStatus" :disabled="faceAccessoryLoading">{{ t('runtimeTools.common.refresh') }}</button>
            <button class="btn-sort" @click="scanFaceAccessory" :disabled="faceAccessoryLoading">{{ t('runtimeTools.common.rescan') }}</button>
          </div>
          <div class="memory-bytes">{{ faceAccessoryStatus.currentBytes || t('runtimeTools.common.notLocated') }}</div>
        </div>

        <div class="memory-card" :class="{ active: infiniteChallengeStatus.enabled }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.challenge.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.challenge.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(infiniteChallengeStatus.rva) }}</span>
            <span>{{ t('runtimeTools.common.status') }}: {{ infiniteChallengeStatus.enabled ? t('runtimeTools.common.enabled') : t('runtimeTools.common.default') }}</span>
          </div>
          <div class="memory-row">
            <button class="btn-batch" @click="setInfiniteChallengeEnabled(true)" :disabled="infiniteChallengeLoading || infiniteChallengeStatus.enabled">{{ t('runtimeTools.misc.challenge.enable') }}</button>
            <button class="btn-refresh" @click="setInfiniteChallengeEnabled(false)" :disabled="infiniteChallengeLoading || !infiniteChallengeStatus.enabled">{{ t('runtimeTools.common.restoreDefault') }}</button>
            <button class="btn-refresh" @click="loadInfiniteChallengeStatus" :disabled="infiniteChallengeLoading">{{ t('runtimeTools.common.refresh') }}</button>
          </div>
          <div class="memory-bytes">{{ infiniteChallengeStatus.currentBytes || t('runtimeTools.common.notRead') }}</div>
        </div>

        <div class="memory-card" :class="{ active: materialConsumeStatus.enabled }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.material.title') }}</span>
            <span class="info-dot" :title="t('runtimeTools.misc.material.notice')">!</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.material.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(materialConsumeStatus.rva) }}</span>
            <span>{{ t('runtimeTools.common.status') }}: {{ materialConsumeStatus.enabled ? t('runtimeTools.common.enabled') : t('runtimeTools.common.default') }}</span>
          </div>
          <div class="memory-row">
            <button class="btn-batch" @click="setMaterialConsumeEnabled(true)" :disabled="materialConsumeLoading || materialConsumeStatus.enabled">{{ t('runtimeTools.misc.material.enable') }}</button>
            <button class="btn-refresh" @click="setMaterialConsumeEnabled(false)" :disabled="materialConsumeLoading || !materialConsumeStatus.enabled">{{ t('runtimeTools.common.restoreDefault') }}</button>
            <button class="btn-refresh" @click="loadMaterialConsumeStatus" :disabled="materialConsumeLoading">{{ t('runtimeTools.common.refresh') }}</button>
          </div>
          <div class="memory-bytes">{{ materialConsumeStatus.currentBytes || t('runtimeTools.common.notRead') }}</div>
        </div>

        <div class="memory-card" :class="{ active: inventorySet45Enabled }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.crab.title') }}</span>
            <span class="info-dot" :title="t('runtimeTools.misc.crab.notice')">!</span>
            <span class="memory-hint">{{ inventorySet45Enabled ? t('runtimeTools.misc.crab.timer', { seconds: inventorySet45Seconds }) : t('runtimeTools.misc.crab.hint') }}</span>
          </div>
          <div class="memory-row">
            <button class="btn-batch" @click="setInventorySet45Enabled(true, 45)" :disabled="inventorySet45Loading || inventorySet45Enabled">{{ t('runtimeTools.misc.crab.normal') }}</button>
            <button class="btn-batch" @click="setInventorySet45Enabled(true, 20)" :disabled="inventorySet45Loading || inventorySet45Enabled">{{ t('runtimeTools.misc.crab.black') }}</button>
            <button class="btn-refresh" @click="setInventorySet45Enabled(false)" :disabled="inventorySet45Loading || !inventorySet45Enabled">{{ t('runtimeTools.misc.crab.restore') }}</button>
            <button class="btn-batch" @click="completeCollectibleTask" :disabled="collectibleTaskLoading">{{ collectibleTaskLoading ? t('runtimeTools.misc.crab.processing') : t('runtimeTools.misc.crab.achievement') }}</button>
          </div>
        </div>

        <div class="memory-card" :class="{ active: terminusDropStatus.enabled }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.terminus.title') }}</span>
            <span class="info-dot" :title="t('runtimeTools.misc.terminus.notice')">!</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.terminus.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(terminusDropStatus.rva) }}</span>
            <span>{{ t('runtimeTools.common.status') }}: {{ terminusDropStatus.enabled ? t('runtimeTools.common.enabled') : t('runtimeTools.common.default') }}</span>
          </div>
          <div class="memory-row">
            <button class="btn-batch" @click="setTerminusDropEnabled(true)" :disabled="terminusDropLoading || terminusDropStatus.enabled">{{ t('runtimeTools.misc.terminus.enable') }}</button>
            <button class="btn-refresh" @click="setTerminusDropEnabled(false)" :disabled="terminusDropLoading || !terminusDropStatus.enabled">{{ t('runtimeTools.common.restoreDefault') }}</button>
            <button class="btn-refresh" @click="loadTerminusDropStatus" :disabled="terminusDropLoading">{{ t('runtimeTools.common.refresh') }}</button>
            <button class="btn-sort" @click="scanTerminusDrop" :disabled="terminusDropLoading">{{ t('runtimeTools.common.rescan') }}</button>
          </div>
          <div class="memory-bytes">{{ terminusDropStatus.currentBytes || t('runtimeTools.common.notLocated') }}</div>
        </div>

        <div v-if="showOutdatedFeatures" class="memory-card" :class="{ active: unlockAllTrophyStatus.enabled }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.trophy.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.trophy.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(unlockAllTrophyStatus.rva) }}</span>
            <span>{{ t('runtimeTools.common.status') }}: {{ unlockAllTrophyStatus.enabled ? t('runtimeTools.common.enabled') : t('runtimeTools.common.default') }}</span>
          </div>
          <div class="memory-row">
            <button class="btn-batch" @click="setUnlockAllTrophyEnabled(true)" :disabled="unlockAllTrophyLoading || unlockAllTrophyStatus.enabled">{{ t('runtimeTools.misc.trophy.enable') }}</button>
            <button class="btn-refresh" @click="setUnlockAllTrophyEnabled(false)" :disabled="unlockAllTrophyLoading || !unlockAllTrophyStatus.enabled">{{ t('runtimeTools.common.restoreDefault') }}</button>
            <button class="btn-refresh" @click="loadUnlockAllTrophyStatus" :disabled="unlockAllTrophyLoading">{{ t('runtimeTools.common.refresh') }}</button>
            <button class="btn-sort" @click="scanUnlockAllTrophy" :disabled="unlockAllTrophyLoading">{{ t('runtimeTools.common.rescan') }}</button>
          </div>
          <div class="memory-bytes">{{ unlockAllTrophyStatus.currentBytes || t('runtimeTools.common.notLocated') }}</div>
        </div>

        <div v-if="showOutdatedFeatures" class="memory-card" :class="{ active: otherSkinPurpleRuneStatus.enabled }">
          <div class="memory-header">
            <span class="memory-title">{{ t('runtimeTools.misc.purpleRune.title') }}</span>
            <span class="memory-hint">{{ t('runtimeTools.misc.purpleRune.hint') }}</span>
          </div>
          <div class="memory-info">
            <span>{{ t('runtimeTools.common.rva') }}: {{ formatHex(otherSkinPurpleRuneStatus.rva) }}</span>
            <span>{{ t('runtimeTools.common.status') }}: {{ otherSkinPurpleRuneStatus.enabled ? t('runtimeTools.common.enabled') : t('runtimeTools.common.default') }}</span>
            <span>{{ t('runtimeTools.misc.purpleRune.jump', { opcode: otherSkinPurpleRuneStatus.jumpOpcode || '—' }) }}</span>
          </div>
          <div class="memory-row">
            <button class="btn-batch" @click="setOtherSkinPurpleRuneEnabled(true)" :disabled="otherSkinPurpleRuneLoading || otherSkinPurpleRuneStatus.enabled">{{ t('runtimeTools.misc.purpleRune.enable') }}</button>
            <button class="btn-refresh" @click="setOtherSkinPurpleRuneEnabled(false)" :disabled="otherSkinPurpleRuneLoading || !otherSkinPurpleRuneStatus.enabled">{{ t('runtimeTools.common.restoreDefault') }}</button>
            <button class="btn-refresh" @click="loadOtherSkinPurpleRuneStatus" :disabled="otherSkinPurpleRuneLoading">{{ t('runtimeTools.common.refresh') }}</button>
          </div>
          <div class="memory-bytes">{{ otherSkinPurpleRuneStatus.currentBytes || t('runtimeTools.common.notRead') }}</div>
        </div>

      </template>
      <div v-else class="empty">{{ t('runtimeTools.messages.connectFirst') }}</div>
    </div>
    <div v-if="showUnlockAllTrophyConfirm" class="confirm-overlay" @click.self="showUnlockAllTrophyConfirm = false">
      <div class="confirm-dialog">
        <div class="confirm-title">{{ t('runtimeTools.misc.trophy.confirmTitle') }}</div>
        <div class="confirm-body">{{ t('runtimeTools.misc.trophy.confirmBody') }}</div>
        <div class="confirm-actions">
          <button class="btn-refresh" @click="showUnlockAllTrophyConfirm = false">{{ t('runtimeTools.common.cancel') }}</button>
          <button class="btn-warn" @click="confirmUnlockAllTrophy" :disabled="unlockAllTrophyLoading">{{ t('runtimeTools.common.confirmEnable') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.root { display:flex; flex-direction:column; gap:10px; width:100%; max-width:720px; margin:0 auto; padding-bottom:40px; }
.section {
  border-radius:16px; padding:16px 18px;
  background:linear-gradient(135deg, rgba(56,189,248,0.12) 0%, rgba(103,232,249,0.06) 100%);
  border:1px solid rgba(103,232,249,0.15);
  display:flex; flex-direction:column; gap:10px;
}
.header { display:flex; align-items:center; justify-content:space-between; gap:8px; }
.title { font-size:0.88rem; font-weight:600; color:rgba(255,255,255,0.65); letter-spacing:1px; }
.info-dot { display:inline-flex; align-items:center; justify-content:center; width:15px; height:15px; border-radius:50%; border:1px solid rgba(103,232,249,0.35); color:#67e8f9; background:rgba(103,232,249,0.08); font-size:0.68rem; font-weight:700; cursor:help; flex-shrink:0; }
.hint { font-size:0.68rem; color:rgba(255,255,255,0.25); margin-left:auto; }
.connect-row { display:flex; align-items:center; gap:10px; }
.btn-connect {
  padding:8px 18px; border-radius:8px; border:1px solid rgba(34,197,94,0.4);
  background:rgba(34,197,94,0.12); color:#4ade80; font-size:0.82rem; font-weight:600; cursor:pointer;
  transition:background 0.2s,transform 0.15s;
}
.btn-connect:not(:disabled):hover { background:rgba(34,197,94,0.22); transform:scale(1.02); }
.btn-connect:disabled { opacity:0.5; cursor:not-allowed; }
.btn-disconnect {
  padding:8px 18px; border-radius:8px; border:1px solid rgba(239,68,68,0.4);
  background:rgba(239,68,68,0.12); color:#f87171; font-size:0.82rem; font-weight:600; cursor:pointer;
  transition:background 0.2s;
}
.btn-disconnect:hover { background:rgba(239,68,68,0.22); }
.pid { font-size:0.72rem; color:rgba(255,255,255,0.35); font-family:'Courier New',monospace; }
.memory-card {
  position:relative; overflow:hidden; z-index:0;
  border-radius:12px; padding:12px;
  background:rgba(255,255,255,0.045); border:1px solid rgba(165,180,252,0.16);
  box-shadow:0 10px 26px rgba(0,0,0,0.18);
  display:flex; flex-direction:column; gap:8px;
  transition:border-color 0.3s, box-shadow 0.3s, transform 0.3s;
}
.memory-card::after {
  content:""; position:absolute; inset:0; z-index:-1; border-radius:12px;
  background:#abd373; transform:translateY(calc(-100% - 2px));
  transition:transform 0.5s ease;
}
.memory-card.active { border-color:rgba(171,211,115,0.55); box-shadow:0 14px 34px rgba(171,211,115,0.18); }
.memory-card.active::after { transform:translateY(0); }
.memory-card.active .memory-title { color:#1f2937; }
.memory-card.active .memory-hint,
.memory-card.active .memory-info,
.memory-card.active .memory-bytes { color:rgba(31,41,55,0.72); }
.memory-card.active .info-dot { border-color:rgba(31,41,55,0.28); color:#1f2937; background:rgba(31,41,55,0.08); }
.memory-card.active .btn-batch { border-color:rgba(31,41,55,0.22); background:rgba(31,41,55,0.12); color:#1f2937; }
.memory-card.active .btn-refresh,
.memory-card.active .btn-sort { border-color:rgba(31,41,55,0.16); background:rgba(255,255,255,0.18); color:rgba(31,41,55,0.72); }
.memory-card.active .batch-input { border-color:rgba(31,41,55,0.22); background:rgba(255,255,255,0.22); color:#1f2937; }
.memory-header, .memory-info, .memory-row { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
.memory-header { justify-content:flex-start; }
.memory-header .memory-hint { margin-left:auto; }
.memory-title { font-size:0.8rem; font-weight:600; color:rgba(255,255,255,0.62); }
.memory-hint, .memory-info { font-size:0.68rem; color:rgba(255,255,255,0.32); }
.memory-bytes { font-size:0.66rem; color:rgba(255,255,255,0.24); font-family:'Courier New',monospace; word-break:break-all; }
.damage-meter-info { justify-content:space-between; }
.damage-meter-value { font-size:1.8rem; font-weight:700; color:#67e8f9; line-height:1; }
.damage-meter-raw { margin-top:-4px; font-size:0.72rem; color:rgba(255,255,255,0.28); }
.memory-card.active .damage-meter-value { color:#1f2937; }
.memory-card.active .damage-meter-raw { color:rgba(31,41,55,0.56); }
.currency-grid { display:flex; flex-direction:column; gap:8px; }
.currency-row { display:grid; grid-template-columns:90px 1fr 120px auto; align-items:center; gap:8px; }
.currency-name { font-size:0.78rem; font-weight:600; color:rgba(255,255,255,0.62); }
.currency-meta { font-size:0.66rem; color:rgba(255,255,255,0.28); font-family:'Courier New',monospace; }
.currency-input { width:120px; }
.coordinate-input { width:100px; }
.memory-card.active .currency-name { color:#1f2937; }
.memory-card.active .currency-meta { color:rgba(31,41,55,0.56); }
.update-new { color:#4ade80; }
.update-body { max-height:86px; overflow-y:auto; padding:8px 10px; border-radius:8px; background:rgba(255,255,255,0.03); color:rgba(255,255,255,0.36); font-size:0.7rem; line-height:1.45; white-space:pre-wrap; scrollbar-width:thin; scrollbar-color:rgba(255,255,255,0.12) transparent; }
.batch-input {
  width:80px; padding:6px 10px; border-radius:6px; border:1px solid rgba(255,255,255,0.15);
  background:rgba(255,255,255,0.07); color:#fff; font-size:0.82rem; outline:none;
}
.countdown-input { width:96px; }
.batch-input:focus { border-color:rgba(103,232,249,0.5); }
.batch-input::-webkit-outer-spin-button, .batch-input::-webkit-inner-spin-button { -webkit-appearance:none; margin:0; }
.btn-batch {
  padding:6px 14px; border-radius:6px; border:1px solid rgba(165,180,252,0.3);
  background:rgba(165,180,252,0.1); color:#a5b4fc; font-size:0.78rem; font-weight:600; cursor:pointer;
  transition:background 0.2s; white-space:nowrap;
}
.btn-batch:not(:disabled):hover { background:rgba(165,180,252,0.2); }
.btn-batch:disabled { opacity:0.4; cursor:not-allowed; }
.btn-refresh, .btn-sort {
  padding:6px 14px; border-radius:6px; border:1px solid rgba(255,255,255,0.12);
  background:rgba(255,255,255,0.05); color:rgba(255,255,255,0.5); font-size:0.78rem; font-weight:600; cursor:pointer;
  transition:background 0.2s;
}
.btn-refresh:hover, .btn-sort:hover { background:rgba(255,255,255,0.1); color:rgba(255,255,255,0.7); }
.btn-refresh:disabled, .btn-sort:disabled { opacity:0.4; cursor:not-allowed; }
.empty { font-size:0.78rem; color:rgba(255,255,255,0.3); text-align:center; padding:12px 0; }
.od-select {
  padding:6px 10px; border-radius:6px; border:1px solid rgba(255,255,255,0.15);
  background:rgba(255,255,255,0.07); color:#fff; font-size:0.8rem; outline:none; cursor:pointer;
}
.od-select:focus { border-color:rgba(103,232,249,0.5); }
.od-select option { background:#1a1a2e; color:#fff; }
.od-indicator {
  font-size:0.72rem; padding:4px 10px; border-radius:6px; text-align:center;
  background:rgba(255,255,255,0.05); color:rgba(255,255,255,0.35);
  transition:all 0.3s;
}
.od-mode-active { background:rgba(250,204,21,0.15); color:#facc15; border:1px solid rgba(250,204,21,0.25); }
.od-burst-active { background:rgba(239,68,68,0.15); color:#ef4444; border:1px solid rgba(239,68,68,0.25); animation:od-burst-pulse 1s infinite alternate; }
@keyframes od-burst-pulse { from { opacity:0.7; } to { opacity:1; } }
.burst-timer { color:#facc15; font-weight:600; font-family:'Courier New',monospace; }
.confirm-overlay { position:fixed; inset:0; z-index:20; display:flex; align-items:center; justify-content:center; padding:20px; background:rgba(0,0,0,0.48); }
.confirm-dialog { width:min(420px, 100%); border-radius:12px; padding:16px; background:linear-gradient(135deg, rgba(251,191,36,0.22) 0%, rgba(239,68,68,0.16) 100%); border:1px solid rgba(251,191,36,0.34); box-shadow:0 12px 40px rgba(0,0,0,0.42); display:flex; flex-direction:column; gap:12px; }
.confirm-title { font-size:0.9rem; font-weight:700; color:#facc15; }
.confirm-body { font-size:0.78rem; line-height:1.65; color:rgba(255,255,255,0.72); }
.confirm-actions { display:flex; justify-content:flex-end; gap:8px; flex-wrap:wrap; }
.btn-warn { padding:6px 14px; border-radius:6px; border:1px solid rgba(251,191,36,0.45); background:rgba(251,191,36,0.16); color:#facc15; font-size:0.78rem; font-weight:600; cursor:pointer; transition:background 0.2s; white-space:nowrap; }
.btn-warn:not(:disabled):hover { background:rgba(251,191,36,0.26); }
.btn-warn:disabled { opacity:0.4; cursor:not-allowed; }
</style>
