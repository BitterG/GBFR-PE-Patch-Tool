package backend

import (
	"strconv"
	"strings"
	"testing"
)

func TestLogsMasteryNodePoolUsesEffectUIIDsOnly(t *testing.T) {
	pools, err := NewApp().LogsMasteryNodePool("PL0400", []uint32{10, 11, 250, 9999})
	if err != nil {
		t.Fatal(err)
	}
	found := map[uint32]LogsMasteryNode{}
	for _, pool := range pools {
		for _, node := range pool.Nodes {
			found[node.EffectUIId] = node
		}
	}
	for _, id := range []uint32{10, 11, 250} {
		if node, ok := found[id]; !ok || !node.Active || node.Unknown {
			t.Fatalf("EffectUiId %d was not active in the Logs layout: %#v", id, node)
		}
	}
	if found[10].Rank != "R1" || found[250].Rank != "EX" {
		t.Fatalf("unexpected ranks: %#v / %#v", found[10], found[250])
	}
	if node, ok := found[9999]; !ok || !node.Active || !node.Unknown {
		t.Fatalf("unknown EffectUiId must be safely retained: %#v", node)
	}
}

func TestMapLogsMasteryEffectUIIDsMapsWithinContextAndKeepsDistinctHashes(t *testing.T) {
	app := NewApp()
	for _, ids := range [][]uint32{{133, 154, 157, 159}, {257, 259}} {
		mapped, err := app.MapLogsMasteryEffectUIIDs("PL0400", ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(mapped.Hashes) != 0 {
			t.Fatalf("insufficient context must not map any IDs %v: %#v", ids, mapped)
		}
		for _, id := range ids {
			if reason := mapped.Unmapped[id]; !strings.Contains(reason, "需"+strconv.Itoa(len(ids))+"/可用1") {
				t.Fatalf("ID %d insufficient reason = %q, want need/available count", id, reason)
			}
		}
	}

	if err := loadLogsSkillboard(); err != nil {
		t.Fatal(err)
	}
	loadSkillboard()
	var selectedID uint32
	var context logsMasteryContext
	for key, ids := range logsSkillboardLayout["pl0400"] {
		rank, ok := logsMasteryRankForLayoutKey(key)
		if !ok {
			continue
		}
		for _, id := range ids {
			text := normalizeLogsMasteryText(logsSkillboardTexts[logsMasteryTextKey("PL0400", id)].Text)
			category, _ := logsMasteryCategory(id)
			candidateCount := 0
			seenHashes := map[uint32]struct{}{}
			for _, node := range skillboardAllNodes {
				nodeRank, _, valid := masteryRankOfGrp(node.Grp)
				hash, hashErr := ParseHashHex(node.Hash)
				if node.Char != "PL0400" || node.Cat != category || nodeRank != rank || !valid || hashErr != nil || (nodeRank == "R1" && strings.TrimSpace(node.Name) != "") || isMasterySpecializationHash(hash) || normalizeLogsMasteryText(node.Desc) != text {
					continue
				}
				seenHashes[hash] = struct{}{}
			}
			candidateCount = len(seenHashes)
			if text != "" && candidateCount > 1 {
				selectedID = id
				context = logsMasteryContext{owner: "PL0400", category: category, rank: rank, text: text}
				break
			}
		}
		if selectedID != 0 {
			break
		}
	}
	if selectedID == 0 {
		t.Fatal("PL0400 has no context with more candidates than one active ID")
	}
	mapped, err := app.MapLogsMasteryEffectUIIDs("PL0400", []uint32{selectedID, selectedID})
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.Hashes) != 1 || len(mapped.Unmapped) != 0 {
		t.Fatalf("duplicate EffectUiId must be deduplicated and map once: %#v", mapped)
	}
	for _, node := range skillboardAllNodes {
		if node.Hash != mapped.Hashes[0] {
			continue
		}
		rank, _, _ := masteryRankOfGrp(node.Grp)
		if node.Char == context.owner && node.Cat == context.category && rank == context.rank && normalizeLogsMasteryText(node.Desc) == context.text {
			return
		}
	}
	t.Fatalf("mapped hash %q is outside its context %#v", mapped.Hashes[0], context)
}

