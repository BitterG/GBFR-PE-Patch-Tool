package backend

import (
	"fmt"
	"sort"
	"strings"
)

type LoadoutComplianceItem struct {
	Index          int    `json:"index"`
	Source         string `json:"source"` // constructed | inventory
	SigilName      string `json:"sigilName"`
	PrimaryTrait   string `json:"primaryTrait"`
	SecondaryTrait string `json:"secondaryTrait,omitempty"`
	Status         string `json:"status"`
	Writable       bool   `json:"writable"`
	Message        string `json:"message"`
}

type LoadoutComplianceReport struct {
	Status   string                  `json:"status"`
	Writable bool                    `json:"writable"`
	Message  string                  `json:"message"`
	Items    []LoadoutComplianceItem `json:"items"`
}

func classifyPreparedLoadoutSigilCompliance(cat *Catalog, draft LoadoutConstructedSigil, prepared *preparedLoadoutSigil) (string, bool, string) {
	if _, exactTransport := exactLoadoutTransportHashID(prepared.item.SigilID); exactTransport {
		return LegalityForced, true, "精确复刻现有因子组合；哈希、词条与等级已通过写入校验，但该记录不在普通天然因子目录中"
	}
	_, legality, legalityErr := (&SigilGen{catalog: cat}).normalizeQueueItem(prepared.item)
	if legalityErr != nil {
		return LegalityUnknown, true, legalityErr.Error()
	}
	if legality.Status != LegalityLegal {
		return legality.Status, legality.Writable, legality.Message
	}
	if _, naturalErr := prepareLoadoutSigilNatural(cat, draft); naturalErr != nil {
		return LegalityForced, true, fmt.Sprintf("偏离自然生成目录：%s", naturalErr)
	}
	return LegalityLegal, true, "主副词条与等级符合本地 DLC 2.0.2 自然生成目录"
}

// LoadoutCheckCompliance is the read-only form of the write preflight. The
// final Writable bit comes from validateLoadoutWrite itself; per-factor rows
// additionally distinguish locally proven construction from an existing save
// instance whose natural drop provenance cannot be reconstructed.
func (a *App) LoadoutCheckCompliance(path string, write LoadoutWrite) (*LoadoutComplianceReport, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("存档路径不能为空")
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	catalog, err := LoadCatalog()
	if err != nil {
		return nil, fmt.Errorf("加载因子合规目录失败: %w", err)
	}
	index := buildLoadoutIndex(save)
	report := &LoadoutComplianceReport{
		Status: LegalityLegal, Writable: true,
		Message: "写入预检通过；构造因子符合本地 DLC 2.0.2 自然目录",
	}

	constructed := make(map[int]LoadoutConstructedSigil, len(write.ConstructedSigils))
	for _, draft := range write.ConstructedSigils {
		item := LoadoutComplianceItem{
			Index: draft.Index, Source: "constructed", SigilName: draft.Item.SigilName,
			PrimaryTrait: draft.Item.PrimaryTraitName, SecondaryTrait: draft.Item.SecondaryTraitName,
			Status: LegalityLegal, Writable: true,
			Message: "主副词条与等级符合本地 DLC 2.0.2 自然生成目录",
		}
		if _, duplicate := constructed[draft.Index]; duplicate {
			item.Status, item.Writable, item.Message = LegalityImpossible, false, fmt.Sprintf("因子槽位 %d 被重复配置", draft.Index+1)
		} else if prepared, prepareErr := prepareLoadoutSigil(catalog, draft); prepareErr != nil {
			item.Status, item.Writable, item.Message = LegalityImpossible, false, prepareErr.Error()
		} else {
			item.SigilName = prepared.item.SigilName
			item.PrimaryTrait = prepared.item.PrimaryTraitName
			item.SecondaryTrait = prepared.item.SecondaryTraitName
			item.Status, item.Writable, item.Message = classifyPreparedLoadoutSigilCompliance(catalog, draft, prepared)
		}
		constructed[draft.Index] = draft
		report.Items = append(report.Items, item)
	}

	for slotIndex, slotID := range write.SigilSlotIDs {
		if slotID == 0 {
			continue
		}
		if _, replaced := constructed[slotIndex]; replaced {
			continue
		}
		name := fmt.Sprintf("存档因子 #%d", slotID)
		if unitID, ok := index.gemBySlotID[slotID]; ok {
			if entry := index.gemHash[unitID]; entry != nil {
				name = sigilDisplayNameOr(entry.Uint32())
			}
		}
		report.Items = append(report.Items, LoadoutComplianceItem{
			Index: slotIndex, Source: "inventory", SigilName: name,
			Status: LegalityUnknown, Writable: true,
			Message: "真实存档现有实例；引用关系会完整预检，但自然掉落来源无法由存档反推",
		})
	}
	sort.SliceStable(report.Items, func(i, j int) bool { return report.Items[i].Index < report.Items[j].Index })

	if _, err := validateLoadoutWrite(save, index, catalog, write); err != nil {
		report.Status = LegalityImpossible
		report.Writable = false
		report.Message = err.Error()
		return report, nil
	}
	for _, item := range report.Items {
		if item.Status == LegalityImpossible {
			report.Status = LegalityImpossible
			report.Writable = false
			report.Message = item.Message
			return report, nil
		}
		if item.Status == LegalityUnknown && report.Status == LegalityLegal {
			report.Status = LegalityUnknown
			report.Message = "写入预检通过；存档现有实例可引用，自然掉落来源未重建"
		}
		if item.Status == LegalityForced && report.Status == LegalityLegal {
			report.Status = LegalityForced
			report.Message = "结构写入检查通过；部分配置偏离自然生成规则，请按需确认"
		}
	}
	return report, nil
}
