package backend

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// ── Save File Constants ──

const XXHash64SaveSeed uint64 = 0x2F1A43EBCD

const (
	SaveID_HashSeed           = 1003
	SaveID_Rupees             = 1104
	SaveID_MasteryPoints      = 1112
	SaveID_Commendations      = 1106
	SaveID_CurrentStageID     = 1201
	SaveID_PartyHealth        = 1206
	SaveID_ItemID             = 1801
	SaveID_ItemCount          = 1802
	SaveID_ItemFlags          = 1803
	SaveID_CurioRewardItemID  = 1901
	SaveID_CurioIDs           = 2002
	SaveID_QuestIDs           = 2570
	SaveID_QuestCompleteCount = 2571
	SaveID_GemID              = 2703
	SaveID_GemWornBy          = 2706
	SaveID_WeaponID           = 2803
	SaveID_WeaponXP           = 2804
	SaveID_CharacterID        = 1301
	SaveID_CharacterQuestUse  = 1314
	SaveID_FavoriteChara      = 4601
	SaveID_BadgeUnlocked      = 5801
	SaveID_BadgeRewardClaimed = 5814
	SaveID_BadgeViewed        = 5816
	SaveID_IsUnlocked         = 7102
)

// ── Data Types ──

type SaveGameFile struct {
	Binary1  *SaveDataBinary `json:"binary1"`
	SlotData *SaveDataBinary `json:"slotData"`
	Hashes   []uint64        `json:"-"`
}

type SaveDataBinary struct {
	VersionMaybe uint32               `json:"versionMaybe"`
	IntTable     []IntSaveDataUnit    `json:"intTable,omitempty"`
	UIntTable    []UIntSaveDataUnit   `json:"uintTable,omitempty"`
	BoolTable    []BoolSaveDataUnit   `json:"boolTable,omitempty"`
	FloatTable   []FloatSaveDataUnit  `json:"floatTable,omitempty"`
	ByteTable    []ByteSaveDataUnit   `json:"byteTable,omitempty"`
	UByteTable   []UByteSaveDataUnit  `json:"ubyteTable,omitempty"`
	ShortTable   []ShortSaveDataUnit  `json:"shortTable,omitempty"`
	UShortTable  []UShortSaveDataUnit `json:"ushortTable,omitempty"`
	LongTable    []LongSaveDataUnit   `json:"longTable,omitempty"`
	ULongTable   []ULongSaveDataUnit  `json:"ulongTable,omitempty"`
}

type IntSaveDataUnit struct {
	IDType    uint32  `json:"idType"`
	UnitID    uint32  `json:"unitID"`
	ValueData []int32 `json:"valueData"`
}
type UIntSaveDataUnit struct {
	IDType    uint32   `json:"idType"`
	UnitID    uint32   `json:"unitID"`
	ValueData []uint32 `json:"valueData"`
}
type BoolSaveDataUnit struct {
	IDType    uint32 `json:"idType"`
	UnitID    uint32 `json:"unitID"`
	ValueData []bool `json:"valueData"`
}
type FloatSaveDataUnit struct {
	IDType    uint32    `json:"idType"`
	UnitID    uint32    `json:"unitID"`
	ValueData []float32 `json:"valueData"`
}
type ByteSaveDataUnit struct {
	IDType    uint32 `json:"idType"`
	UnitID    uint32 `json:"unitID"`
	ValueData []byte `json:"valueData"`
}
type UByteSaveDataUnit struct {
	IDType    uint32 `json:"idType"`
	UnitID    uint32 `json:"unitID"`
	ValueData []byte `json:"valueData"`
}
type ShortSaveDataUnit struct {
	IDType    uint32  `json:"idType"`
	UnitID    uint32  `json:"unitID"`
	ValueData []int16 `json:"valueData"`
}
type UShortSaveDataUnit struct {
	IDType    uint32   `json:"idType"`
	UnitID    uint32   `json:"unitID"`
	ValueData []uint16 `json:"valueData"`
}
type LongSaveDataUnit struct {
	IDType    uint32  `json:"idType"`
	UnitID    uint32  `json:"unitID"`
	ValueData []int64 `json:"valueData"`
}
type ULongSaveDataUnit struct {
	IDType    uint32   `json:"idType"`
	UnitID    uint32   `json:"unitID"`
	ValueData []uint64 `json:"valueData"`
}

// ── Save loading ──

func LoadSaveFile(path string) (*SaveGameFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	return ParseSaveData(data)
}

func ParseSaveData(data []byte) (*SaveGameFile, error) {
	if len(data) < 52 {
		return nil, fmt.Errorf("文件太小，不是有效的存档")
	}

	_ = binary.LittleEndian.Uint32(data[0:4])
	_ = binary.LittleEndian.Uint64(data[4:12])
	_ = binary.LittleEndian.Uint32(data[12:16])
	_ = binary.LittleEndian.Uint32(data[16:20])
	offset1 := int64(binary.LittleEndian.Uint64(data[20:28]))
	slotDataOffset := int64(binary.LittleEndian.Uint64(data[28:36]))
	size1 := int64(binary.LittleEndian.Uint64(data[36:44]))
	slotDataSize := int64(binary.LittleEndian.Uint64(data[44:52]))

	save := &SaveGameFile{}

	if offset1 > 0 && size1 > 0 {
		if !validInt64Span(offset1, size1, int64(len(data))) {
			return nil, fmt.Errorf("存档头 binary1 偏移无效")
		}
		binary1, err := parseSaveDataBinary(data[offset1 : offset1+size1])
		if err == nil {
			save.Binary1 = binary1
		}
	}

	if slotDataOffset > 0 && slotDataSize > 0 {
		if !validInt64Span(slotDataOffset, slotDataSize, int64(len(data))) {
			return nil, fmt.Errorf("存档头 slot-data 偏移无效")
		}
		slotBuffer := data[slotDataOffset : slotDataOffset+slotDataSize]
		slotData, err := parseSaveDataBinary(slotBuffer)
		if err != nil {
			return nil, fmt.Errorf("解析SlotData失败: %w", err)
		}
		save.SlotData = slotData

		if len(slotBuffer) > 0x14 {
			hashesOffset := binary.LittleEndian.Uint32(slotBuffer[len(slotBuffer)-0x14:])
			if int(hashesOffset)+8*10 <= len(slotBuffer) {
				save.Hashes = make([]uint64, 10)
				for i := 0; i < 10; i++ {
					save.Hashes[i] = binary.LittleEndian.Uint64(slotBuffer[hashesOffset+uint32(i*8):])
				}
			}
		}
	}

	return save, nil
}

func validInt64Span(offset, size, total int64) bool {
	return offset >= 0 && size >= 0 && offset <= total && size <= total-offset
}

func validIntSpan(offset, size, total int) bool {
	return offset >= 0 && size >= 0 && offset <= total && size <= total-offset
}

// ── FlatBuffers reader ──

type fbReader struct {
	data []byte
}

func (r *fbReader) u32(pos int) uint32 {
	if !validIntSpan(pos, 4, len(r.data)) {
		return 0
	}
	return binary.LittleEndian.Uint32(r.data[pos : pos+4])
}

func (r *fbReader) i32(pos int) int32 {
	return int32(r.u32(pos))
}

func (r *fbReader) u16(pos int) uint16 {
	if !validIntSpan(pos, 2, len(r.data)) {
		return 0
	}
	return binary.LittleEndian.Uint16(r.data[pos : pos+2])
}

// readVectorAt reads a FlatBuffers vector at the given table+field position.
// Returns (count, dataStart). elementSize is the encoded size of one vector item.
func (r *fbReader) readVectorAt(tablePos int, fieldOff uint16, elementSize int) (int, int) {
	const maxSaveVectorElements = 1 << 20
	if elementSize <= 0 || !validIntSpan(tablePos, int(fieldOff), len(r.data)) {
		return 0, 0
	}
	fieldPos := tablePos + int(fieldOff)
	if !validIntSpan(fieldPos, 4, len(r.data)) {
		return 0, 0
	}
	vecOff := uint64(r.u32(fieldPos))
	vecPos64 := uint64(fieldPos) + vecOff
	if vecPos64 > uint64(len(r.data)) || vecPos64 > uint64(^uint(0)>>1) {
		return 0, 0
	}
	vecPos := int(vecPos64)
	if !validIntSpan(vecPos, 4, len(r.data)) {
		return 0, 0
	}
	count := int(r.u32(vecPos))
	if count < 0 || count > maxSaveVectorElements || count > (len(r.data)-(vecPos+4))/elementSize {
		return 0, 0
	}
	return count, vecPos + 4
}

// readSubTable reads a table element at elementPos.
// Returns (tableStart, vtablePos, vtableSize, objectSize).
func (r *fbReader) readSubTable(elementPos int) (int, int, uint16, uint16) {
	if !validIntSpan(elementPos, 4, len(r.data)) {
		return 0, 0, 0, 0
	}
	soff := int32(r.u32(elementPos))
	vpos64 := int64(elementPos) - int64(soff)
	if vpos64 < 0 || vpos64 > int64(len(r.data)) {
		return 0, 0, 0, 0
	}
	vpos := int(vpos64)
	if !validIntSpan(vpos, 4, len(r.data)) {
		return 0, 0, 0, 0
	}
	vs := r.u16(vpos)
	os := r.u16(vpos + 2)
	if vs < 4 || os < 4 || !validIntSpan(vpos, int(vs), len(r.data)) || !validIntSpan(elementPos, int(os), len(r.data)) {
		return 0, 0, 0, 0
	}
	return elementPos, vpos, vs, os
}

// fieldOff returns the offset of a field within the object, or 0 if absent.
func (r *fbReader) fieldOff(vpos int, vsize uint16, fieldIdx int) (uint16, bool) {
	off := 4 + fieldIdx*2
	if fieldIdx < 0 || !validIntSpan(off, 2, int(vsize)) || !validIntSpan(vpos, off+2, len(r.data)) {
		return 0, false
	}
	fo := r.u16(vpos + off)
	return fo, fo != 0
}

// ── SaveDataBinary root parser ──

func parseSaveDataBinary(data []byte) (*SaveDataBinary, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("数据太小")
	}
	rootOff := binary.LittleEndian.Uint32(data[0:4])
	if uint64(rootOff)+4 > uint64(len(data)) {
		return nil, fmt.Errorf("root offset超出范围")
	}
	r := &fbReader{data: data}
	result := &SaveDataBinary{}

	tpos := int(rootOff)
	soff := int32(r.u32(tpos))
	vpos64 := int64(tpos) - int64(soff)
	if vpos64 < 0 || vpos64 > int64(len(data)) {
		return nil, fmt.Errorf("根表vtable无效")
	}
	vpos := int(vpos64)
	if !validIntSpan(vpos, 4, len(data)) {
		return nil, fmt.Errorf("根表vtable无效")
	}
	vsize := r.u16(vpos)
	if vsize < 4 || !validIntSpan(vpos, int(vsize), len(data)) {
		return nil, fmt.Errorf("根表vtable无效")
	}

	// Field 0: VersionMaybe
	if fo, ok := r.fieldOff(vpos, vsize, 0); ok {
		result.VersionMaybe = r.u32(tpos + int(fo))
	}
	// Fields 1-10: typed tables
	if fo, ok := r.fieldOff(vpos, vsize, 1); ok {
		result.BoolTable = parseBoolUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 2); ok {
		result.ByteTable = parseByteUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 3); ok {
		result.UByteTable = parseUByteUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 4); ok {
		result.ShortTable = parseShortUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 5); ok {
		result.UShortTable = parseUShortUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 6); ok {
		result.IntTable = parseIntUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 7); ok {
		result.UIntTable = parseUIntUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 8); ok {
		result.LongTable = parseLongUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 9); ok {
		result.ULongTable = parseULongUnits(r, tpos, fo)
	}
	if fo, ok := r.fieldOff(vpos, vsize, 10); ok {
		result.FloatTable = parseFloatUnits(r, tpos, fo)
	}

	return result, nil
}

// ── Generic table vector parser ──

// In FlatBuffers, a vector of tables stores u32 offsets (relative to each offset position).
// Each offset points to the actual table, which has: [vtable_soffset:i32] [field_data...]
// vtable tells us field positions; we don't need object_size since each element is independently located.

func parseUIntUnits(r *fbReader, tpos int, fo uint16) []UIntSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]UIntSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := UIntSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 4); vc > 0 {
				u.ValueData = make([]uint32, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = r.u32(vd + j*4)
				}
			}
		}
		result = append(result, u)
	}
	return result
}

// tableVectorReader parses a FlatBuffers vector of tables.
// Each element is a u32 offset pointing to the actual table data.
type tableVectorReader struct {
	r     *fbReader
	count int
	start int // position in buffer where offset array starts
}

func makeTableVec(r *fbReader, tpos int, fo uint16) *tableVectorReader {
	count, start := r.readVectorAt(tpos, fo, 4)
	if count == 0 {
		return nil
	}
	return &tableVectorReader{r: r, count: count, start: start}
}

// read reads the next table element. Returns (tableStart, vtablePos, vtableSize).
func (v *tableVectorReader) read(i int) (int, int, uint16) {
	offPos := v.start + i*4
	if offPos+4 > len(v.r.data) {
		return 0, 0, 0
	}
	elemOff := int32(v.r.u32(offPos))
	elemPos := offPos + int(elemOff)
	ts, vp, vs, _ := v.r.readSubTable(elemPos)
	return ts, vp, vs
}

func parseIntUnits(r *fbReader, tpos int, fo uint16) []IntSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]IntSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := IntSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 4); vc > 0 {
				u.ValueData = make([]int32, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = r.i32(vd + j*4)
				}
			}
		}
		result = append(result, u)
	}
	return result
}

func parseBoolUnits(r *fbReader, tpos int, fo uint16) []BoolSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]BoolSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := BoolSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 1); vc > 0 {
				u.ValueData = make([]bool, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = r.data[vd+j] != 0
				}
			}
		}
		result = append(result, u)
	}
	return result
}

func parseFloatUnits(r *fbReader, tpos int, fo uint16) []FloatSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]FloatSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := FloatSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 4); vc > 0 {
				u.ValueData = make([]float32, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = math.Float32frombits(r.u32(vd + j*4))
				}
			}
		}
		result = append(result, u)
	}
	return result
}

func parseByteUnits(r *fbReader, tpos int, fo uint16) []ByteSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]ByteSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := ByteSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 1); vc > 0 {
				u.ValueData = make([]byte, vc)
				copy(u.ValueData, r.data[vd:vd+vc])
			}
		}
		result = append(result, u)
	}
	return result
}

func parseUByteUnits(r *fbReader, tpos int, fo uint16) []UByteSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]UByteSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := UByteSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 1); vc > 0 {
				u.ValueData = make([]byte, vc)
				copy(u.ValueData, r.data[vd:vd+vc])
			}
		}
		result = append(result, u)
	}
	return result
}

func parseShortUnits(r *fbReader, tpos int, fo uint16) []ShortSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]ShortSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := ShortSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 2); vc > 0 {
				u.ValueData = make([]int16, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = int16(r.u16(vd + j*2))
				}
			}
		}
		result = append(result, u)
	}
	return result
}

func parseUShortUnits(r *fbReader, tpos int, fo uint16) []UShortSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]UShortSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := UShortSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 2); vc > 0 {
				u.ValueData = make([]uint16, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = r.u16(vd + j*2)
				}
			}
		}
		result = append(result, u)
	}
	return result
}

func parseLongUnits(r *fbReader, tpos int, fo uint16) []LongSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]LongSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := LongSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 8); vc > 0 {
				u.ValueData = make([]int64, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = int64(binary.LittleEndian.Uint64(r.data[vd+j*8 : vd+(j+1)*8]))
				}
			}
		}
		result = append(result, u)
	}
	return result
}

func parseULongUnits(r *fbReader, tpos int, fo uint16) []ULongSaveDataUnit {
	tv := makeTableVec(r, tpos, fo)
	if tv == nil {
		return nil
	}
	result := make([]ULongSaveDataUnit, 0, tv.count)
	for i := 0; i < tv.count; i++ {
		ts, vp, vs := tv.read(i)
		if vs == 0 {
			continue
		}
		u := ULongSaveDataUnit{}
		if f, ok := r.fieldOff(vp, vs, 0); ok {
			u.IDType = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 1); ok {
			u.UnitID = r.u32(ts + int(f))
		}
		if f, ok := r.fieldOff(vp, vs, 2); ok {
			if vc, vd := r.readVectorAt(ts, f, 8); vc > 0 {
				u.ValueData = make([]uint64, vc)
				for j := 0; j < vc; j++ {
					u.ValueData[j] = binary.LittleEndian.Uint64(r.data[vd+j*8 : vd+(j+1)*8])
				}
			}
		}
		result = append(result, u)
	}
	return result
}

// ── Query helpers ──

func (s *SaveDataBinary) GetUIntUnit(idType uint32) *UIntSaveDataUnit {
	for i := range s.UIntTable {
		if s.UIntTable[i].IDType == idType {
			return &s.UIntTable[i]
		}
	}
	return nil
}

func (s *SaveDataBinary) GetIntUnit(idType uint32) *IntSaveDataUnit {
	for i := range s.IntTable {
		if s.IntTable[i].IDType == idType {
			return &s.IntTable[i]
		}
	}
	return nil
}

func (s *SaveDataBinary) GetBoolUnit(idType uint32) *BoolSaveDataUnit {
	for i := range s.BoolTable {
		if s.BoolTable[i].IDType == idType {
			return &s.BoolTable[i]
		}
	}
	return nil
}
