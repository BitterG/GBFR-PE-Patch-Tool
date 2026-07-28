package main

import "testing"

func TestQuestIDToNameUsesHexadecimalQuestIDs(t *testing.T) {
	for _, test := range []struct {
		questID uint32
		wantEN  string
		wantCN  string
	}{
		{questID: 0x401303, wantEN: "Worried about Papa", wantCN: "担心爸爸"},
		{questID: 0x401318, wantEN: "Worried about Papa", wantCN: "担心爸爸"},
	} {
		t.Run(test.wantEN, func(t *testing.T) {
			if got := questIDToName(test.questID); got != test.wantEN {
				t.Errorf("questIDToName(%#x) = %q, want %q", test.questID, got, test.wantEN)
			}
			if got := questIDToNameCN(test.questID); got != test.wantCN {
				t.Errorf("questIDToNameCN(%#x) = %q, want %q", test.questID, got, test.wantCN)
			}
		})
	}
}
