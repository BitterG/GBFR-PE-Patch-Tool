package backend

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"golang.org/x/sys/windows"
)

const (
	HashSeedIDType   uint32 = 1003
	TraitHashIDType  uint32 = 1701
	TraitLevelIDType uint32 = 1702
	GemMaxSlotIDType uint32 = 2701
	GemSlotIDType    uint32 = 2702
	GemIDType        uint32 = 2703
	GemLevelIDType   uint32 = 2704
	GemWornByIDType  uint32 = 2706
	GemFlagsIDType   uint32 = 2707
	EmptyHash        uint32 = 0x887AE0B0
	NormalSigilFlags uint32 = 2
	SaveHashSeed     uint64 = 0x2F1A43EBCD
	GemSlotBaseID           = 30000
	TraitSlotBase           = 120000000
)

var hashSections = []struct{ start, subSize int }{
	{0x58, 0x80}, {0x30, 0xA0}, {0x28, 0x30}, {0x38, 0xC0}, {0x40, 0xB0},
	{0x68, 0x50}, {0x48, 0x60}, {0x70, 0x90}, {0x50, 0x40}, {0x60, 0x70},
}

// unitEntry holds the position and value info for one FlatBuffer unit entry.
type unitEntry struct {
	IDType   uint32
	UnitID   uint32
	ValueOff int // absolute offset in data where ValueData[0] lives
	ValueCnt int // number of elements in ValueData vector
	data     []byte
}

func (e *unitEntry) Uint32() uint32 {
	if e.ValueOff < 0 || e.ValueOff+4 > len(e.data) {
		return 0
	}
	return binary.LittleEndian.Uint32(e.data[e.ValueOff:])
}

func (e *unitEntry) Int32() int32 {
	if e.ValueOff < 0 || e.ValueOff+4 > len(e.data) {
		return 0
	}
	return int32(binary.LittleEndian.Uint32(e.data[e.ValueOff:]))
}

func (e *unitEntry) SetUint32(v uint32) {
	binary.LittleEndian.PutUint32(e.data[e.ValueOff:], v)
}

func (e *unitEntry) SetInt32(v int32) {
	binary.LittleEndian.PutUint32(e.data[e.ValueOff:], uint32(v))
}

func (e *unitEntry) Uint32At(index int) (uint32, error) {
	if index < 0 || index >= e.ValueCnt || e.ValueOff+index*4+4 > len(e.data) {
		return 0, fmt.Errorf("save unit 索引超出范围: %d/%d", index, e.ValueCnt)
	}
	return binary.LittleEndian.Uint32(e.data[e.ValueOff+index*4:]), nil
}

func (e *unitEntry) SetUint32At(index int, value uint32) error {
	if index < 0 || index >= e.ValueCnt || e.ValueOff+index*4+4 > len(e.data) {
		return fmt.Errorf("save unit 索引超出范围: %d/%d", index, e.ValueCnt)
	}
	binary.LittleEndian.PutUint32(e.data[e.ValueOff+index*4:], value)
	return nil
}

func (e *unitEntry) SetInt32At(index int, value int32) error {
	return e.SetUint32At(index, uint32(value))
}

// Bytes 返回**字节向量**（如配装名称 3002，Byte 表 ValueCnt=字节数）的原始字节切片。
// 仅对字节类型条目有效：字节向量里 ValueCnt 就是字节数、元素步长为 1。
// 绝不可对 uint32 向量调用——那里 ValueCnt 是元素个数、每元素 4 字节，会只取到 1/4 数据。
// 返回的是底层缓冲的切片（可原地改），越界时返回 nil。
func (e *unitEntry) Bytes() []byte {
	if e == nil || e.ValueOff < 0 || e.ValueCnt < 0 || e.ValueOff+e.ValueCnt > len(e.data) {
		return nil
	}
	return e.data[e.ValueOff : e.ValueOff+e.ValueCnt]
}

// SetBytes 把 buf 写入字节向量：先把整块 ValueCnt 字节清零、再拷贝 buf。
// len(buf) > ValueCnt 报错（拒绝越界写，否则会冲毁相邻 FlatBuffer 记录）。
// 用于配装名称等 Byte 表字段——名称短于 64 字节时其余自动补 0x00，
// 从而覆盖空槽可能残留的旧名尾巴，并让读侧靠首个 NUL 正确截断。
func (e *unitEntry) SetBytes(buf []byte) error {
	region := e.Bytes()
	if region == nil {
		return fmt.Errorf("字节向量越界: off=%d cnt=%d len=%d", e.ValueOff, e.ValueCnt, len(e.data))
	}
	if len(buf) > len(region) {
		return fmt.Errorf("写入 %d 字节超过向量容量 %d", len(buf), len(region))
	}
	for i := range region {
		region[i] = 0
	}
	copy(region, buf)
	return nil
}

func (e *unitEntry) Bool() bool {
	if e.ValueOff < 0 || e.ValueOff >= len(e.data) {
		return false
	}
	return e.data[e.ValueOff] != 0
}

func (e *unitEntry) SetBool(v bool) {
	if v {
		e.data[e.ValueOff] = 1
	} else {
		e.data[e.ValueOff] = 0
	}
}

type SaveData struct {
	data           []byte
	slotOff        int64
	slotLen        int64
	path           string
	lastBackupPath string
	unitsByType    map[uint32][]*unitEntry
}

func LoadSave(path string) (*SaveData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取存档失败: %w", err)
	}
	if len(data) < 0x34 {
		return nil, fmt.Errorf("存档文件太小")
	}

	slotOff := int64(binary.LittleEndian.Uint64(data[0x1C:0x24]))
	slotLen := int64(binary.LittleEndian.Uint64(data[0x2C:0x34]))
	if slotLen <= 0 || !validInt64Span(slotOff, slotLen, int64(len(data))) {
		return nil, fmt.Errorf("存档头 slot-data 偏移无效")
	}

	return &SaveData{data: data, slotOff: slotOff, slotLen: slotLen, path: path}, nil
}

func (s *SaveData) slotSpan() []byte {
	return s.data[s.slotOff : s.slotOff+s.slotLen]
}

// findUnit finds a single FlatBuffer unit entry by IDType + UnitID.
func (s *SaveData) findUnit(idType, unitID uint32) (*unitEntry, bool) {
	slot := s.slotSpan()
	slotBase := int(s.slotOff)

	for _, step := range []int{4, 1} {
		for off := 4; off < len(slot)-16; off += step {
			entry, ok := tryReadUnitEntry(slot, off, idType, unitID)
			if !ok {
				continue
			}
			entry.ValueOff += slotBase
			entry.data = s.data
			return entry, true
		}
	}
	return nil, false
}

// findAllUnitsByType finds all FlatBuffer unit entries matching a specific IDType.
func (s *SaveData) findAllUnitsByType(idType uint32) []*unitEntry {
	if cached, ok := s.unitsByType[idType]; ok {
		return cached
	}
	slot := s.slotSpan()
	slotBase := int(s.slotOff)
	seen := make(map[int]bool)
	var results []*unitEntry

	idBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(idBytes, idType)

	// Strategy 1: raw byte scan for IDType value, then locate table start nearby
	for i := 0; i < len(slot)-16; i++ {
		if slot[i] != idBytes[0] || slot[i+1] != idBytes[1] ||
			slot[i+2] != idBytes[2] || slot[i+3] != idBytes[3] {
			continue
		}
		// Found IDType at slot[i], search backward up to 20 bytes for table start
		searchStart := i - 20
		if searchStart < 0 {
			searchStart = 0
		}
		for tableOff := searchStart; tableOff <= i; tableOff++ {
			if seen[tableOff] {
				continue
			}
			entry, ok := tryReadUnitEntry(slot, tableOff, idType, 0)
			if ok && entry.IDType == idType {
				seen[tableOff] = true
				entry.ValueOff += slotBase
				entry.data = s.data
				results = append(results, entry)
				break
			}
		}
	}

	// Strategy 2: fallback 4-byte aligned scan (for entries missed by strategy 1)
	for off := 4; off < len(slot)-16; off += 4 {
		if seen[off] {
			continue
		}
		entry, ok := tryReadUnitEntry(slot, off, idType, 0)
		if !ok || entry.IDType != idType {
			continue
		}
		entry.ValueOff += slotBase
		entry.data = s.data
		results = append(results, entry)
	}
	if s.unitsByType == nil {
		s.unitsByType = make(map[uint32][]*unitEntry)
	}
	s.unitsByType[idType] = results
	return results
}

// findUnitExact also distinguishes UnitID 0 from "no UnitID filter". Item
// inventory slots start at zero, so using findUnit(idType, 0) for those fields
// could silently address a different table entry.
func (s *SaveData) findUnitExact(idType, unitID uint32) (*unitEntry, bool) {
	for _, entry := range s.findAllUnitsByType(idType) {
		if entry.UnitID == unitID {
			return entry, true
		}
	}
	return nil, false
}

// tryReadUnitEntry attempts to read a FlatBuffer UIntSaveDataUnit/IntSaveDataUnit at off.
// If unitID is non-zero, also filters by UnitID. Returns the entry and whether it matched.
func tryReadUnitEntry(slot []byte, off int, idType, unitID uint32) (*unitEntry, bool) {
	vtableDist := int32(binary.LittleEndian.Uint32(slot[off:]))
	if vtableDist == 0 {
		return nil, false
	}

	candidates := []int{off - int(vtableDist), off + int(vtableDist)}
	for _, vtOff := range candidates {
		if vtOff < 0 || vtOff > len(slot)-10 {
			continue
		}
		vtableSize := binary.LittleEndian.Uint16(slot[vtOff:])
		objectSize := binary.LittleEndian.Uint16(slot[vtOff+2:])
		if vtableSize < 10 || objectSize < 4 || int(vtableSize) > 256 || int(objectSize) > len(slot)-off {
			continue
		}

		idField := binary.LittleEndian.Uint16(slot[vtOff+4:])
		dataField := binary.LittleEndian.Uint16(slot[vtOff+8:])
		if idField == 0 || dataField == 0 {
			continue
		}
		if int(idField) > len(slot)-off-4 || int(dataField) > len(slot)-off-4 {
			continue
		}

		foundID := binary.LittleEndian.Uint32(slot[off+int(idField):])
		if foundID != idType {
			continue
		}

		// UnitID field is optional — check if it exists (vtable offset != 0)
		var foundUnitID uint32
		unitField := binary.LittleEndian.Uint16(slot[vtOff+6:])
		if unitField != 0 {
			if int(unitField) > len(slot)-off-4 {
				continue
			}
			foundUnitID = binary.LittleEndian.Uint32(slot[off+int(unitField):])
		}
		// If filtering by a specific unitID, check match
		if unitID != 0 && foundUnitID != unitID {
			continue
		}

		vectorFieldOff := off + int(dataField)
		relVectorOff := binary.LittleEndian.Uint32(slot[vectorFieldOff:])
		vectorOff := vectorFieldOff + int(relVectorOff)
		if vectorOff < 0 || vectorOff > len(slot)-8 {
			continue
		}
		count := int32(binary.LittleEndian.Uint32(slot[vectorOff:]))
		if count <= 0 {
			continue
		}

		return &unitEntry{
			IDType:   foundID,
			UnitID:   foundUnitID,
			ValueOff: vectorOff + 4,
			ValueCnt: int(count),
		}, true
	}
	return nil, false
}

// GetMaxSlotID returns the current max sigil slot ID.
func (s *SaveData) GetMaxSlotID() (int, error) {
	entry, ok := s.findUnit(GemMaxSlotIDType, 0)
	if !ok {
		return 0, fmt.Errorf("找不到 GEMDATA_MAX_SLOT_ID (2701)")
	}
	return int(entry.Uint32()), nil
}

// SetMaxSlotID writes a new max sigil slot ID.
func (s *SaveData) SetMaxSlotID(id int) error {
	entry, ok := s.findUnit(GemMaxSlotIDType, 0)
	if !ok {
		return fmt.Errorf("找不到 GEMDATA_MAX_SLOT_ID (2701)")
	}
	entry.SetUint32(uint32(id))
	return nil
}

// FindEmptyGemSlots returns up to `count` empty sigil slot unit IDs.
// An empty slot has hash == EmptyHash (0x887AE0B0).
func (s *SaveData) FindEmptyGemSlots(count int) ([]int, error) {
	allGemUnits := s.findAllUnitsByType(GemIDType)
	var emptyIDs []int
	for _, u := range allGemUnits {
		if int(u.UnitID) >= GemSlotBaseID && u.Uint32() == EmptyHash {
			emptyIDs = append(emptyIDs, int(u.UnitID))
			if len(emptyIDs) >= count {
				break
			}
		}
	}
	if len(emptyIDs) < count {
		return nil, fmt.Errorf("空因子槽不足 (需要 %d, 找到 %d)", count, len(emptyIDs))
	}
	return emptyIDs, nil
}

// GetOccupiedGemCount returns the number of non-empty sigil slots.
func (s *SaveData) GetOccupiedGemCount() int {
	allGemUnits := s.findAllUnitsByType(GemIDType)
	count := 0
	for _, u := range allGemUnits {
		if int(u.UnitID) >= GemSlotBaseID && u.Uint32() != EmptyHash {
			count++
		}
	}
	return count
}

// PatchSigil writes a complete sigil into a slot, replacing whatever was there.
func (s *SaveData) PatchSigil(gemUnitID, newSlotID int, sigilHash uint32, level int,
	primaryTraitHash uint32, primaryLevel int,
	secondaryTraitHash uint32, secondaryLevel int, hasSecondary bool) error {
	return s.PatchSigilWithFlags(gemUnitID, newSlotID, sigilHash, level, primaryTraitHash, primaryLevel, secondaryTraitHash, secondaryLevel, hasSecondary, NormalSigilFlags)
}

// PatchSigilWithFlags duplicates a game-accepted real-save template without
// normalising its record flags. Ordinary catalog construction continues to use
// PatchSigil and NormalSigilFlags.
func (s *SaveData) PatchSigilWithFlags(gemUnitID, newSlotID int, sigilHash uint32, level int,
	primaryTraitHash uint32, primaryLevel int,
	secondaryTraitHash uint32, secondaryLevel int, hasSecondary bool, flags uint32) error {

	gemIndex := gemUnitID - GemSlotBaseID
	primaryTraitUnit := TraitSlotBase + (gemIndex * 100)
	secondaryTraitUnit := primaryTraitUnit + 1

	// Every patch is applied through the same error-checked helper. A missing
	// save unit here must surface as an error to the caller (which can abort
	// before FixChecksums and leave the buffer untouched); panicking inside a
	// Wails-bound save-write path would crash the whole app instead.
	writes := []func() error{
		// --- Gem slot fields ---
		func() error { return s.patchUint(GemSlotIDType, uint32(gemUnitID), uint32(newSlotID)) },
		func() error { return s.patchUint(GemIDType, uint32(gemUnitID), sigilHash) },
		func() error { return s.patchInt(GemLevelIDType, uint32(gemUnitID), level) },
		func() error { return s.patchUint(GemWornByIDType, uint32(gemUnitID), EmptyHash) },
		func() error { return s.patchUint(GemFlagsIDType, uint32(gemUnitID), flags) },
		// --- Trait fields ---
		func() error { return s.patchUint(TraitHashIDType, uint32(primaryTraitUnit), primaryTraitHash) },
		func() error { return s.patchInt(TraitLevelIDType, uint32(primaryTraitUnit), primaryLevel) },
	}
	if hasSecondary {
		writes = append(writes,
			func() error { return s.patchUint(TraitHashIDType, uint32(secondaryTraitUnit), secondaryTraitHash) },
			func() error { return s.patchInt(TraitLevelIDType, uint32(secondaryTraitUnit), secondaryLevel) },
		)
	}
	for _, write := range writes {
		if err := write(); err != nil {
			return err
		}
	}
	return nil
}

// ClearSigil zeroes out a sigil slot.
func (s *SaveData) ClearSigil(gemUnitID int) error {
	if gemUnitID < GemSlotBaseID {
		return fmt.Errorf("因子槽 UnitID 无效: %d", gemUnitID)
	}
	gemIndex := gemUnitID - GemSlotBaseID
	primaryTraitUnit := TraitSlotBase + (gemIndex * 100)
	secondaryTraitUnit := primaryTraitUnit + 1

	type clearWrite struct {
		idType uint32
		unitID uint32
		value  uint32
		entry  *unitEntry
	}
	writes := []clearWrite{
		{idType: GemIDType, unitID: uint32(gemUnitID), value: EmptyHash},
		{idType: GemLevelIDType, unitID: uint32(gemUnitID)},
		{idType: GemWornByIDType, unitID: uint32(gemUnitID), value: EmptyHash},
		{idType: GemFlagsIDType, unitID: uint32(gemUnitID)},
		{idType: TraitHashIDType, unitID: uint32(primaryTraitUnit), value: EmptyHash},
		{idType: TraitLevelIDType, unitID: uint32(primaryTraitUnit)},
		{idType: TraitHashIDType, unitID: uint32(secondaryTraitUnit), value: EmptyHash},
		{idType: TraitLevelIDType, unitID: uint32(secondaryTraitUnit)},
	}
	for index := range writes {
		entry, ok := s.findUnit(writes[index].idType, writes[index].unitID)
		if !ok {
			return fmt.Errorf("清空因子前置校验失败: 找不到 save unit: IDType=%d, UnitID=%d", writes[index].idType, writes[index].unitID)
		}
		writes[index].entry = entry
	}
	for _, write := range writes {
		write.entry.SetUint32(write.value)
	}
	return nil
}

func allocateSequentialSlotIDs(currentMax, count int) (first, last int, err error) {
	if currentMax < 0 {
		return 0, 0, fmt.Errorf("当前最大 SlotID 无效: %d", currentMax)
	}
	if count <= 0 {
		return 0, 0, fmt.Errorf("待分配 SlotID 数量无效: %d", count)
	}
	last64 := uint64(currentMax) + uint64(count)
	if uint64(currentMax) > math.MaxUint32 || last64 > math.MaxUint32 {
		return 0, 0, fmt.Errorf("SlotID 空间不足: 当前最大值 %d，新增 %d 个将超过 uint32", currentMax, count)
	}
	first64 := uint64(currentMax) + 1
	if first64 <= uint64(EmptyHash) && last64 >= uint64(EmptyHash) {
		return 0, 0, fmt.Errorf("SlotID 分配区间会占用保留空值 0x%08X", EmptyHash)
	}
	return int(first64), int(last64), nil
}

func (s *SaveData) patchUint(idType, unitID, value uint32) error {
	entry, ok := s.findUnit(idType, unitID)
	if !ok {
		return fmt.Errorf("找不到 save unit: IDType=%d, UnitID=%d", idType, unitID)
	}
	entry.SetUint32(value)
	return nil
}

func (s *SaveData) patchUintExact(idType, unitID, value uint32) error {
	entry, ok := s.findUnitExact(idType, unitID)
	if !ok {
		return fmt.Errorf("找不到精确 save unit: IDType=%d, UnitID=%d", idType, unitID)
	}
	entry.SetUint32(value)
	return nil
}

func (s *SaveData) patchInt(idType, unitID uint32, value int) error {
	if int64(value) < math.MinInt32 || int64(value) > math.MaxInt32 {
		return fmt.Errorf("save int32 值越界: %d", value)
	}
	entry, ok := s.findUnit(idType, unitID)
	if !ok {
		return fmt.Errorf("找不到 save unit: IDType=%d, UnitID=%d", idType, unitID)
	}
	entry.SetInt32(int32(value))
	return nil
}

// FixChecksums recomputes XXHash64 for the hash-protected sections.
func (s *SaveData) FixChecksums() error {
	slot := s.slotSpan()

	// Read hash seed (IDType 1003)
	seedEntry, ok := s.findUnit(HashSeedIDType, 0)
	if !ok {
		return fmt.Errorf("找不到 SAVEDATA_HASHSEED (1003)")
	}
	idx := int(seedEntry.Uint32() % uint32(len(hashSections)))

	// Hash table offset is stored at (slotLen - 0x14)
	if int(s.slotLen) < 0x14 {
		return fmt.Errorf("slot data 太小，无 hash table")
	}
	hashesOff := int(binary.LittleEndian.Uint32(slot[s.slotLen-0x14:]))
	if hashesOff+(len(hashSections)*8) > int(s.slotLen) {
		return fmt.Errorf("hash table 偏移超出 slot data 范围")
	}

	section := hashSections[idx]
	hashStart := section.start
	hashLen := hashesOff - (section.start + section.subSize)
	if hashLen <= 0 || hashStart+hashLen > len(slot) {
		return fmt.Errorf("hash 区间无效")
	}

	d := xxhash.NewWithSeed(SaveHashSeed)
	d.Write(slot[hashStart : hashStart+hashLen])
	hash := d.Sum64()
	binary.LittleEndian.PutUint64(slot[hashesOff+idx*8:], hash)
	return nil
}

// Write saves via a temporary file. When overwriting the loaded save it first
// creates a timestamped backup, so an interrupted write cannot destroy the
// only usable copy.
func (s *SaveData) Write(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	groupSnapshot, err := autoSnapshotBeforeSaveWrite(path)
	if err != nil {
		return fmt.Errorf("创建整组存档快照失败，已取消写入: %w", err)
	}

	s.lastBackupPath = ""
	if groupSnapshot.ID != "" {
		if root, rootErr := saveSnapshotRoot(); rootErr == nil {
			s.lastBackupPath = filepath.Join(root, groupSnapshot.ID)
		}
	}
	inputAbs, _ := filepath.Abs(s.path)
	outputAbs, _ := filepath.Abs(path)
	if strings.EqualFold(filepath.Clean(inputAbs), filepath.Clean(outputAbs)) {
		backupPath := fmt.Sprintf("%s.pre-edit-%s.bak", path, time.Now().Format("20060102-150405.000"))
		if err := copyFile(s.path, backupPath); err != nil {
			return fmt.Errorf("创建存档备份失败: %w", err)
		}
		if err := pruneTimestampedFileBackups(path, saveSnapshotRetention); err != nil {
			return fmt.Errorf("清理旧存档备份失败: %w", err)
		}
		if s.lastBackupPath == "" {
			s.lastBackupPath = backupPath
		}
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时存档失败: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(s.data); err != nil {
		return fmt.Errorf("写入临时存档失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("同步临时存档失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时存档失败: %w", err)
	}
	if err := replaceFileAtomic(tmpPath, path); err != nil {
		return fmt.Errorf("替换目标存档失败: %w", err)
	}
	ok = true
	return nil
}

func replaceFileAtomic(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func (s *SaveData) LastBackupPath() string { return s.lastBackupPath }

func pruneTimestampedFileBackups(path string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("备份保留数量必须至少为 1")
	}
	matches, err := filepath.Glob(path + ".pre-edit-*.bak")
	if err != nil {
		return err
	}
	sort.Strings(matches)
	if len(matches) <= keep {
		return nil
	}
	for _, backup := range matches[:len(matches)-keep] {
		if filepath.Dir(backup) != filepath.Dir(path) || !strings.HasPrefix(filepath.Base(backup), filepath.Base(path)+".pre-edit-") {
			return fmt.Errorf("拒绝清理目标存档目录之外的备份")
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(destination)
		return err
	}
	return out.Close()
}

// VerifySigil re-reads a sigil slot and checks all fields match expected values.
func (s *SaveData) VerifySigil(gemUnitID int, expectedSlotID, sigilHash uint32, level int,
	primaryHash uint32, primaryLevel int,
	secondaryHash uint32, secondaryLevel int, hasSecondary bool) error {
	return s.VerifySigilWithFlags(gemUnitID, expectedSlotID, sigilHash, level, primaryHash, primaryLevel, secondaryHash, secondaryLevel, hasSecondary, NormalSigilFlags)
}

func (s *SaveData) VerifySigilWithFlags(gemUnitID int, expectedSlotID, sigilHash uint32, level int,
	primaryHash uint32, primaryLevel int,
	secondaryHash uint32, secondaryLevel int, hasSecondary bool, flags uint32) error {

	gemIndex := gemUnitID - GemSlotBaseID
	primaryTraitUnit := TraitSlotBase + (gemIndex * 100)
	secondaryTraitUnit := primaryTraitUnit + 1

	check := func(idType, unitID, expected uint32, label string) error {
		entry, ok := s.findUnit(idType, unitID)
		if !ok {
			return fmt.Errorf("验证失败: 找不到 %s", label)
		}
		actual := entry.Uint32()
		if actual != expected {
			return fmt.Errorf("验证失败 %s: 期望 0x%08X, 实际 0x%08X", label, expected, actual)
		}
		return nil
	}
	checkInt := func(idType, unitID uint32, expected int, label string) error {
		entry, ok := s.findUnit(idType, unitID)
		if !ok {
			return fmt.Errorf("验证失败: 找不到 %s", label)
		}
		actual := entry.Int32()
		if int(actual) != expected {
			return fmt.Errorf("验证失败 %s: 期望 %d, 实际 %d", label, expected, actual)
		}
		return nil
	}

	if err := check(GemSlotIDType, uint32(gemUnitID), expectedSlotID, "因子槽位ID"); err != nil {
		return err
	}
	if err := check(GemIDType, uint32(gemUnitID), sigilHash, "因子哈希"); err != nil {
		return err
	}
	if err := checkInt(GemLevelIDType, uint32(gemUnitID), level, "因子等级"); err != nil {
		return err
	}
	if err := check(GemWornByIDType, uint32(gemUnitID), EmptyHash, "装备角色"); err != nil {
		return err
	}
	if err := check(GemFlagsIDType, uint32(gemUnitID), flags, "因子标记"); err != nil {
		return err
	}
	if err := check(TraitHashIDType, uint32(primaryTraitUnit), primaryHash, "主特性哈希"); err != nil {
		return err
	}
	if err := checkInt(TraitLevelIDType, uint32(primaryTraitUnit), primaryLevel, "主特性等级"); err != nil {
		return err
	}
	if hasSecondary {
		if err := check(TraitHashIDType, uint32(secondaryTraitUnit), secondaryHash, "副特性哈希"); err != nil {
			return err
		}
		if err := checkInt(TraitLevelIDType, uint32(secondaryTraitUnit), secondaryLevel, "副特性等级"); err != nil {
			return err
		}
	}
	return nil
}

// tryPatchUint attempts to write a uint value, returning nil if the entry is missing.
func (s *SaveData) tryPatchUint(idType, unitID, value uint32) error {
	entry, ok := s.findUnit(idType, unitID)
	if !ok {
		return nil // missing entry is non-fatal for bulk operations
	}
	entry.SetUint32(value)
	return nil
}

// tryPatchInt attempts to write an int value, returning nil if the entry is missing.
func (s *SaveData) tryPatchInt(idType, unitID uint32, value int) error {
	if int64(value) < math.MinInt32 || int64(value) > math.MaxInt32 {
		return fmt.Errorf("save int32 值越界: %d", value)
	}
	entry, ok := s.findUnit(idType, unitID)
	if !ok {
		return nil
	}
	entry.SetInt32(int32(value))
	return nil
}
