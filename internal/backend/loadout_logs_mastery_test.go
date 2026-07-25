package backend

import "testing"

func TestLogsMasteryNodePoolUsesEffectUIIDsOnly(t *testing.T) {
	pools, err := NewApp().LogsMasteryNodePool("PL0400", []uint32{10, 11, 250, 9999})
	if err != nil { t.Fatal(err) }
	found := map[uint32]LogsMasteryNode{}
	for _, pool := range pools { for _, node := range pool.Nodes { found[node.EffectUIId] = node } }
	for _, id := range []uint32{10, 11, 250} { if node, ok := found[id]; !ok || !node.Active || node.Unknown { t.Fatalf("EffectUiId %d was not active in the Logs layout: %#v", id, node) } }
	if found[10].Rank != "R1" || found[250].Rank != "EX" { t.Fatalf("unexpected ranks: %#v / %#v", found[10], found[250]) }
	if node, ok := found[9999]; !ok || !node.Active || !node.Unknown { t.Fatalf("unknown EffectUiId must be safely retained: %#v", node) }
}
