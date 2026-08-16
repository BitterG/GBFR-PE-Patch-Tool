// Sigil legality rules ported from relink-logs (src-tauri/src/legality/sigils.rs).
// Only the definitely-illegal cases are judged: locked character-sigil pairs,
// single-trait sigils, and quest-locked (crab) traits. An ordinary V+ second
// trait is never accused, and a single-trait sigil's level is never judged.

export const EMPTY_HASH = 0x887ae0b0

export function hex8(v) {
  return '0x' + (Number(v) >>> 0).toString(16).toUpperCase().padStart(8, '0')
}

export function isEmptyHash(v) {
  const n = Number(v)
  return !n || (n >>> 0) === EMPTY_HASH
}

// Returns [{ rule, observedHash, allowedHashes? }] for one sigil's trait pair.
export function checkSigilLegality({ sigilHash, primaryTraitHash, secondaryTraitHash, legality }) {
  const issues = []
  if (!legality) return issues

  const sh = hex8(sigilHash)

  // Locked pairs: a character sigil fixes both traits.
  const pair = legality.lockedPairs?.[sh]
  if (pair && pair.length === 2) {
    if (!isEmptyHash(primaryTraitHash) && hex8(primaryTraitHash) !== pair[0]) {
      issues.push({ rule: 'lockedPairPrimary', observedHash: hex8(primaryTraitHash), allowedHashes: [pair[0]] })
    }
    if (!isEmptyHash(secondaryTraitHash) && hex8(secondaryTraitHash) !== pair[1]) {
      issues.push({ rule: 'lockedPairSecondary', observedHash: hex8(secondaryTraitHash), allowedHashes: [pair[1]] })
    }
  } else {
    // Intrinsic primary-trait match (generator-only, stricter than relink-logs).
    const intrinsic = legality.intrinsicPrimaryTraits?.[sh]
    if (intrinsic && !isEmptyHash(primaryTraitHash) && hex8(primaryTraitHash) !== intrinsic) {
      issues.push({ rule: 'primaryTraitMismatch', observedHash: hex8(primaryTraitHash), allowedHashes: [intrinsic] })
    }
  }

  // Single-trait sigils can never carry a second trait.
  if (!isEmptyHash(secondaryTraitHash) && legality.singleTraitSigils?.includes(sh)) {
    issues.push({ rule: 'singleTrait', observedHash: hex8(secondaryTraitHash) })
  }

  // Quest-locked (crab) traits must stay on their own sigils.
  for (const th of [primaryTraitHash, secondaryTraitHash]) {
    if (isEmptyHash(th)) continue
    const hh = hex8(th)
    const homes = legality.questLockedHomes?.[hh]
    if (homes && !homes.includes(sh)) {
      issues.push({ rule: 'questLockedTrait', observedHash: hh, allowedHashes: homes })
    }
  }

  return issues
}
