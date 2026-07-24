package backend

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type runtimePatchMemory interface {
	ReadCode(addr uintptr, size int) ([]byte, error)
	WriteCode(addr uintptr, data []byte) error
}

type runtimePatchPatchState uint8

const (
	runtimePatchPatchEnabled runtimePatchPatchState = iota + 1
	runtimePatchPatchRecovery
)

type runtimePatchPatchSiteLease struct {
	Address  uintptr
	RVA      uint64
	Original []byte
	Patch    []byte
}

type runtimePatchPatchLease struct {
	FeatureID  string
	OwnerToken string
	Process    processInstanceID
	State      runtimePatchPatchState
	Sites      []runtimePatchPatchSiteLease
}

// RuntimePatchFeatureStatus is the live state of one independently verified
// game-version patch. Arrays are always non-nil for stable Wails JSON shapes.
type RuntimePatchFeatureStatus struct {
	ID           string   `json:"id"`
	Enabled      bool     `json:"enabled"`
	Available    bool     `json:"available"`
	RVAs         []uint64 `json:"rvas"`
	CurrentBytes []string `json:"currentBytes"`
	Error        string   `json:"error"`
}

func cloneRuntimePatchSiteLease(site runtimePatchPatchSiteLease) runtimePatchPatchSiteLease {
	return runtimePatchPatchSiteLease{
		Address:  site.Address,
		RVA:      site.RVA,
		Original: append([]byte(nil), site.Original...),
		Patch:    append([]byte(nil), site.Patch...),
	}
}

func validateRuntimePatchOwnedLease(lease runtimePatchPatchLease, token string, current processInstanceID) error {
	if !runtimeOwnerTokenMatches(lease.OwnerToken, token) {
		return errRuntimeOwnerLeaseStale
	}
	if !sameProcessInstance(lease.Process, current) {
		return fmt.Errorf("RuntimePatch patch belongs to a replaced game process")
	}
	return nil
}

func findRuntimePatchCatalogConflict(feature RuntimePatchFeature, leases map[string]runtimePatchPatchLease) string {
	for _, conflictID := range feature.Conflicts {
		if _, active := leases[conflictID]; active {
			return conflictID
		}
	}
	if feature.ConflictGroup == "" {
		return ""
	}
	catalog, err := loadRuntimePatchCatalog()
	if err != nil {
		return ""
	}
	for _, other := range catalog.Features {
		if other.ID == feature.ID || other.ConflictGroup != feature.ConflictGroup {
			continue
		}
		if _, active := leases[other.ID]; active {
			return other.ID
		}
	}
	return ""
}

func findRuntimePatchActiveAddressOverlap(sites []runtimePatchPatchSiteLease, leases map[string]runtimePatchPatchLease, skipFeatureID string) string {
	for _, site := range sites {
		end := site.Address + uintptr(len(site.Original))
		for featureID, lease := range leases {
			if featureID == skipFeatureID {
				continue
			}
			for _, active := range lease.Sites {
				activeEnd := active.Address + uintptr(len(active.Original))
				if site.Address < activeEnd && active.Address < end {
					return featureID
				}
			}
		}
	}
	return ""
}

func validateRuntimePatchSiteRanges(sites []runtimePatchPatchSiteLease) error {
	for index, site := range sites {
		if site.Address == 0 || len(site.Original) == 0 || len(site.Original) != len(site.Patch) {
			return fmt.Errorf("RuntimePatch site[%d] has invalid address or byte lengths", index)
		}
		if bytes.Equal(site.Original, site.Patch) {
			return fmt.Errorf("RuntimePatch site[%d] original bytes already equal the enable patch", index)
		}
		end := site.Address + uintptr(len(site.Original))
		if end < site.Address {
			return fmt.Errorf("RuntimePatch site[%d] address range overflows", index)
		}
		for previous := 0; previous < index; previous++ {
			other := sites[previous]
			otherEnd := other.Address + uintptr(len(other.Original))
			if site.Address < otherEnd && other.Address < end {
				return fmt.Errorf("RuntimePatch site[%d] overlaps site[%d]", index, previous)
			}
		}
	}
	return nil
}

// installRuntimePatchSites performs a complete read-only preflight before the
// first write, then installs each site atomically. Any later failure restores
// every site already touched in reverse order.
func installRuntimePatchSites(memory runtimePatchMemory, sites []runtimePatchPatchSiteLease) error {
	if memory == nil {
		return fmt.Errorf("RuntimePatch memory is nil")
	}
	if len(sites) == 0 {
		return fmt.Errorf("RuntimePatch patch sites are empty")
	}
	if err := validateRuntimePatchSiteRanges(sites); err != nil {
		return err
	}
	for index, site := range sites {
		current, err := memory.ReadCode(site.Address, len(site.Original))
		if err != nil {
			return fmt.Errorf("preflight RuntimePatch site[%d]: %w", index, err)
		}
		if !bytes.Equal(current, site.Original) {
			return fmt.Errorf("preflight RuntimePatch site[%d] contains foreign bytes: %s", index, bytesToHex(current))
		}
	}

	for index, site := range sites {
		writer := func(data []byte) error { return memory.WriteCode(site.Address, data) }
		reader := func() ([]byte, error) { return memory.ReadCode(site.Address, len(site.Original)) }
		_, installErr := installCodeHookAtomic(site.Original, site.Patch, writer, reader)
		if installErr == nil {
			continue
		}
		rollbackErr := restoreRuntimePatchSites(memory, sites[:index+1])
		return errors.Join(fmt.Errorf("install RuntimePatch site[%d]: %w", index, installErr), rollbackErr)
	}

	for index, site := range sites {
		current, err := memory.ReadCode(site.Address, len(site.Patch))
		if err == nil && bytes.Equal(current, site.Patch) {
			continue
		}
		if err == nil {
			err = fmt.Errorf("current bytes are %s", bytesToHex(current))
		}
		rollbackErr := restoreRuntimePatchSites(memory, sites)
		return errors.Join(fmt.Errorf("verify installed RuntimePatch site[%d]: %w", index, err), rollbackErr)
	}
	return nil
}

// restoreRuntimePatchSites restores in reverse order and never overwrites bytes
// that are neither this lease's complete patch nor its complete original.
func restoreRuntimePatchSites(memory runtimePatchMemory, sites []runtimePatchPatchSiteLease) error {
	if memory == nil {
		return errors.Join(fmt.Errorf("RuntimePatch memory is nil"), errLiveMemoryRollbackUnproven)
	}
	var restoreErr error
	for index := len(sites) - 1; index >= 0; index-- {
		site := sites[index]
		if site.Address == 0 || len(site.Original) == 0 || len(site.Original) != len(site.Patch) {
			restoreErr = errors.Join(restoreErr,
				fmt.Errorf("restore RuntimePatch site[%d]: invalid recovery record", index),
				errLiveMemoryRollbackUnproven)
			continue
		}
		current, err := memory.ReadCode(site.Address, len(site.Original))
		if err != nil {
			restoreErr = errors.Join(restoreErr,
				fmt.Errorf("restore RuntimePatch site[%d] preflight: %w", index, err),
				errLiveMemoryRollbackUnproven)
			continue
		}
		if bytes.Equal(current, site.Original) {
			continue
		}
		if !bytes.Equal(current, site.Patch) {
			restoreErr = errors.Join(restoreErr,
				fmt.Errorf("restore RuntimePatch site[%d] refused foreign bytes: %s", index, bytesToHex(current)),
				errLiveMemoryRollbackUnproven)
			continue
		}

		writer := func(data []byte) error { return memory.WriteCode(site.Address, data) }
		reader := func() ([]byte, error) { return memory.ReadCode(site.Address, len(site.Original)) }
		_, err = installCodeHookAtomic(site.Patch, site.Original, writer, reader)
		if err == nil {
			continue
		}
		// installCodeHookAtomic's result describes its "original" input (the
		// enabled patch), not the restoration target. A fresh read is the
		// only way to prove that this site's original bytes were restored.
		current, proofErr := memory.ReadCode(site.Address, len(site.Original))
		if proofErr == nil && bytes.Equal(current, site.Original) {
			continue
		}
		if proofErr == nil {
			proofErr = fmt.Errorf("current bytes are %s", bytesToHex(current))
		}
		restoreErr = errors.Join(restoreErr,
			fmt.Errorf("restore RuntimePatch site[%d]: %w; proof failed: %v", index, err, proofErr),
			errLiveMemoryRollbackUnproven)
	}
	return restoreErr
}

type runtimePatchProcessMemory struct{ handle windows.Handle }

func (memory runtimePatchProcessMemory) ReadCode(addr uintptr, size int) ([]byte, error) {
	if memory.handle == 0 || addr == 0 || size <= 0 {
		return nil, fmt.Errorf("invalid RuntimePatch process memory read")
	}
	buf := make([]byte, size)
	if err := readProcessMemory(memory.handle, addr, unsafe.Pointer(&buf[0]), uintptr(size)); err != nil {
		return nil, err
	}
	return buf, nil
}

func (memory runtimePatchProcessMemory) WriteCode(addr uintptr, data []byte) error {
	if memory.handle == 0 || addr == 0 || len(data) == 0 {
		return fmt.Errorf("invalid RuntimePatch process memory write")
	}
	return writeCodeMemory(memory.handle, addr, data)
}

func removeRuntimePatchPatchOrderID(order []string, id string) []string {
	filtered := order[:0]
	for _, current := range order {
		if current != id {
			filtered = append(filtered, current)
		}
	}
	return filtered
}

// restoreAllRuntimePatchPatchLeases restores feature order in reverse; the site
// helper independently restores each feature's sites in reverse. Failed
// records remain in both map and order for a later recovery attempt.
func restoreAllRuntimePatchPatchLeases(memory runtimePatchMemory, leases map[string]runtimePatchPatchLease, order *[]string, process processInstanceID, owner string) error {
	if order == nil {
		return errors.Join(fmt.Errorf("RuntimePatch patch order is nil"), errLiveMemoryRollbackUnproven)
	}
	seen := make(map[string]struct{}, len(*order))
	for _, id := range *order {
		if _, exists := leases[id]; !exists {
			return errors.Join(fmt.Errorf("RuntimePatch patch order contains unknown feature %s", id), errLiveMemoryRollbackUnproven)
		}
		if _, duplicate := seen[id]; duplicate {
			return errors.Join(fmt.Errorf("RuntimePatch patch order contains duplicate feature %s", id), errLiveMemoryRollbackUnproven)
		}
		seen[id] = struct{}{}
	}
	for id := range leases {
		if _, ordered := seen[id]; !ordered {
			return errors.Join(fmt.Errorf("RuntimePatch recovery lease %s is missing from patch order", id), errLiveMemoryRollbackUnproven)
		}
	}
	var joined error
	for index := len(*order) - 1; index >= 0; index-- {
		id := (*order)[index]
		lease, exists := leases[id]
		if !exists || (owner != "" && lease.OwnerToken != owner) {
			continue
		}
		if !sameProcessInstance(lease.Process, process) {
			joined = errors.Join(joined,
				fmt.Errorf("RuntimePatch %s belongs to a different process instance", id),
				errLiveMemoryRollbackUnproven)
			continue
		}
		lease.State = runtimePatchPatchRecovery
		leases[id] = lease
		if err := restoreRuntimePatchSites(memory, lease.Sites); err != nil {
			joined = errors.Join(joined, fmt.Errorf("restore RuntimePatch %s: %w", id, err))
			continue
		}
		delete(leases, id)
		*order = removeRuntimePatchPatchOrderID(*order, id)
	}
	return joined
}

func appendRuntimePatchStatusError(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}

func buildRuntimePatchFeatureStatuses(features []RuntimePatchFeature, memory runtimePatchMemory, connected bool, owner string, process processInstanceID, leases map[string]runtimePatchPatchLease) []RuntimePatchFeatureStatus {
	statuses := make([]RuntimePatchFeatureStatus, 0, len(features))
	for _, feature := range features {
		status := RuntimePatchFeatureStatus{
			ID:           feature.ID,
			Available:    connected,
			RVAs:         make([]uint64, 0),
			CurrentBytes: make([]string, 0),
		}
		lease, active := leases[feature.ID]
		if !active {
			if conflict := findRuntimePatchCatalogConflict(feature, leases); conflict != "" {
				status.Available = false
				status.Error = fmt.Sprintf("conflicts with active RuntimePatch feature %s", conflict)
			}
			statuses = append(statuses, status)
			continue
		}

		status.RVAs = make([]uint64, len(lease.Sites))
		status.CurrentBytes = make([]string, len(lease.Sites))
		for index, site := range lease.Sites {
			status.RVAs[index] = site.RVA
		}
		if err := validateRuntimePatchOwnedLease(lease, owner, process); err != nil {
			status.Available = false
			status.Error = err.Error()
			statuses = append(statuses, status)
			continue
		}
		if lease.State != runtimePatchPatchEnabled {
			status.Error = appendRuntimePatchStatusError(status.Error, "recovery is required")
		}
		allPatched := len(lease.Sites) != 0
		for index, site := range lease.Sites {
			current, err := memory.ReadCode(site.Address, len(site.Patch))
			if err != nil {
				allPatched = false
				status.Error = appendRuntimePatchStatusError(status.Error, fmt.Sprintf("site[%d] read failed: %v", index, err))
				continue
			}
			status.CurrentBytes[index] = bytesToHex(current)
			if !bytes.Equal(current, site.Patch) {
				allPatched = false
				status.Error = appendRuntimePatchStatusError(status.Error, fmt.Sprintf("site[%d] contains foreign or restored bytes", index))
			}
		}
		status.Enabled = lease.State == runtimePatchPatchEnabled && allPatched && status.Error == ""
		statuses = append(statuses, status)
	}
	return statuses
}

func findRuntimePatchCatalogFeature(catalog *RuntimePatchCatalog, id string) *RuntimePatchFeature {
	if catalog == nil {
		return nil
	}
	for index := range catalog.Features {
		if catalog.Features[index].ID == strings.TrimSpace(id) {
			return &catalog.Features[index]
		}
	}
	return nil
}

func cloneRuntimePatchPatchLease(lease runtimePatchPatchLease) runtimePatchPatchLease {
	cloned := lease
	cloned.Sites = make([]runtimePatchPatchSiteLease, len(lease.Sites))
	for index, site := range lease.Sites {
		cloned.Sites[index] = cloneRuntimePatchSiteLease(site)
	}
	return cloned
}

// restoreAllRuntimePatchPatchesLocked is called with procMu and runtimePatchMu held.
// It deliberately does not acquire liveMemoryWriteMu because charaDetachLocked
// already runs inside the process lifecycle critical section.
func (a *App) restoreAllRuntimePatchPatchesLocked(owner string) error {
	if len(a.runtimePatchPatchLeases) == 0 && len(a.runtimePatchPatchOrder) == 0 {
		return nil
	}
	err := restoreAllRuntimePatchPatchLeases(
		runtimePatchProcessMemory{handle: a.hProcess},
		a.runtimePatchPatchLeases,
		&a.runtimePatchPatchOrder,
		a.currentProcessInstance(),
		owner,
	)
	if len(a.runtimePatchPatchLeases) == 0 {
		a.runtimePatchPatchLeases = nil
		a.runtimePatchPatchOrder = nil
	}
	if errors.Is(err, errLiveMemoryRollbackUnproven) {
		a.poisonCurrentLiveMemoryWrites()
	}
	return err
}

func (a *App) dropRuntimePatchPatchesForOwnerLocked(owner string) {
	if owner == "" {
		return
	}
	for id, lease := range a.runtimePatchPatchLeases {
		if lease.OwnerToken == owner {
			delete(a.runtimePatchPatchLeases, id)
			a.runtimePatchPatchOrder = removeRuntimePatchPatchOrderID(a.runtimePatchPatchOrder, id)
		}
	}
	if len(a.runtimePatchPatchLeases) == 0 {
		a.runtimePatchPatchLeases = nil
		a.runtimePatchPatchOrder = nil
	}
}

func (a *App) validateRuntimePatchStatusOwnerLocked(token string) error {
	if !runtimeOwnerTokenMatches(a.charaOwnerToken, token) {
		return errRuntimeOwnerLeaseStale
	}
	if a.hProcess == 0 || a.moduleBase == 0 || a.charaPID == 0 || a.charaCreated == 0 || !processHandleAlive(a.hProcess) {
		return fmt.Errorf("game process connection is no longer live")
	}
	return nil
}

// RuntimePatchGetStatusesOwned reads only already-owned sites. Inactive features are
// never pattern-scanned while serving a status request.
func (a *App) RuntimePatchGetStatusesOwned(token string) ([]RuntimePatchFeatureStatus, error) {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if err := a.validateRuntimePatchStatusOwnerLocked(token); err != nil {
		return nil, err
	}
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	catalog, err := loadRuntimePatchCatalog()
	if err != nil {
		return nil, err
	}
	return buildRuntimePatchFeatureStatuses(
		catalog.Features,
		runtimePatchProcessMemory{handle: a.hProcess},
		true,
		token,
		a.currentProcessInstance(),
		a.runtimePatchPatchLeases,
	), nil
}

func prepareRuntimePatchSiteLease(moduleBase, moduleEnd, address uintptr, definition RuntimePatchSite, original []byte) (runtimePatchPatchSiteLease, error) {
	if moduleBase == 0 || moduleEnd <= moduleBase || address < moduleBase || len(definition.EnableBytes) == 0 {
		return runtimePatchPatchSiteLease{}, fmt.Errorf("RuntimePatch patch site has invalid module bounds, address, or patch bytes")
	}
	end := address + uintptr(len(definition.EnableBytes))
	if end < address || end > moduleEnd {
		return runtimePatchPatchSiteLease{}, fmt.Errorf("RuntimePatch patch site target is outside module bounds")
	}
	if len(original) != len(definition.EnableBytes) {
		return runtimePatchPatchSiteLease{}, fmt.Errorf("RuntimePatch captured original length does not match enable patch")
	}
	if len(definition.ExpectedOriginalBytes) != len(definition.EnableBytes) {
		return runtimePatchPatchSiteLease{}, fmt.Errorf("RuntimePatch expected original length does not match enable patch")
	}
	if !bytes.Equal(original, definition.ExpectedOriginalBytes) {
		return runtimePatchPatchSiteLease{}, fmt.Errorf("RuntimePatch runtime bytes do not match locked expected original bytes")
	}
	if len(definition.DisableBytes) != 0 && !bytes.Equal(original, definition.DisableBytes) {
		return runtimePatchPatchSiteLease{}, fmt.Errorf("RuntimePatch explicit disable bytes do not match runtime bytes")
	}
	if bytes.Equal(original, definition.EnableBytes) {
		return runtimePatchPatchSiteLease{}, fmt.Errorf("RuntimePatch runtime bytes already equal the enable patch; ownership cannot be claimed")
	}
	return runtimePatchPatchSiteLease{
		Address:  address,
		RVA:      uint64(address - moduleBase),
		Original: append([]byte(nil), definition.ExpectedOriginalBytes...),
		Patch:    append([]byte(nil), definition.EnableBytes...),
	}, nil
}

func (a *App) prepareRuntimePatchSitesLocked(feature RuntimePatchFeature, memory runtimePatchMemory) ([]runtimePatchPatchSiteLease, error) {
	moduleSize, err := getRemoteModuleSize(a.hProcess, a.moduleBase)
	if err != nil {
		return nil, fmt.Errorf("read RuntimePatch module bounds: %w", err)
	}
	moduleEnd := a.moduleBase + moduleSize
	if moduleEnd < a.moduleBase {
		return nil, fmt.Errorf("RuntimePatch module range overflows")
	}
	sites := make([]runtimePatchPatchSiteLease, 0, len(feature.Sites))
	matchBySignature := make(map[string]uintptr, len(feature.Sites))
	for index, definition := range feature.Sites {
		signatureKey := definition.Module + "\x00" + definition.Symbol + "\x00" + definition.AOB
		match, cached := matchBySignature[signatureKey]
		if !cached {
			pattern := runtimePatchPattern{
				Values: append([]byte(nil), definition.PatternValues...),
				Mask:   append([]byte(nil), definition.PatternMasks...),
			}
			match, err = a.scanRuntimePatchPatternUnique(pattern, fmt.Sprintf("%s site[%d] %s", feature.ID, index, definition.Symbol))
			if err != nil {
				return nil, err
			}
			matchBySignature[signatureKey] = match
		}
		offset := uintptr(definition.Offset)
		address := match + offset
		if address < match {
			return nil, fmt.Errorf("%s site[%d] target address overflows", feature.ID, index)
		}
		end := address + uintptr(len(definition.EnableBytes))
		if end < address || address < a.moduleBase || end > moduleEnd {
			return nil, fmt.Errorf("%s site[%d] target is outside module bounds", feature.ID, index)
		}
		original, err := memory.ReadCode(address, len(definition.EnableBytes))
		if err != nil {
			return nil, fmt.Errorf("read %s site[%d] original bytes: %w", feature.ID, index, err)
		}
		lease, err := prepareRuntimePatchSiteLease(a.moduleBase, moduleEnd, address, definition, original)
		if err != nil {
			return nil, fmt.Errorf("prepare %s site[%d]: %w", feature.ID, index, err)
		}
		sites = append(sites, lease)
	}
	if err := validateRuntimePatchSiteRanges(sites); err != nil {
		return nil, err
	}
	return sites, nil
}

func (a *App) runtimePatchStatusForFeatureLocked(feature RuntimePatchFeature, token string, memory runtimePatchMemory) RuntimePatchFeatureStatus {
	return buildRuntimePatchFeatureStatuses(
		[]RuntimePatchFeature{feature}, memory, true, token,
		a.currentProcessInstance(), a.runtimePatchPatchLeases,
	)[0]
}

// RuntimePatchSetEnabledOwned atomically enables or restores every site belonging to
// a catalog feature while holding global -> process -> runtime patch locks.
func (a *App) RuntimePatchSetEnabledOwned(token, id string, enabled bool) (RuntimePatchFeatureStatus, error) {
	empty := RuntimePatchFeatureStatus{
		ID:           strings.TrimSpace(id),
		RVAs:         make([]uint64, 0),
		CurrentBytes: make([]string, 0),
	}
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return empty, err
	}
	defer a.procMu.Unlock()
	if enabled {
		if err := a.ensureLiveMemoryWritesSafe(); err != nil {
			return empty, err
		}
	}
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()

	catalog, err := loadRuntimePatchCatalog()
	if err != nil {
		return empty, err
	}
	feature := findRuntimePatchCatalogFeature(catalog, id)
	if feature == nil {
		return empty, fmt.Errorf("unknown RuntimePatch feature: %s", strings.TrimSpace(id))
	}
	memory := runtimePatchProcessMemory{handle: a.hProcess}
	process := a.currentProcessInstance()
	lease, active := a.runtimePatchPatchLeases[feature.ID]

	if !enabled {
		if !active {
			return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), nil
		}
		if err := validateRuntimePatchOwnedLease(lease, token, process); err != nil {
			return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), err
		}
		lease.State = runtimePatchPatchRecovery
		a.runtimePatchPatchLeases[feature.ID] = cloneRuntimePatchPatchLease(lease)
		if err := restoreRuntimePatchSites(memory, lease.Sites); err != nil {
			if errors.Is(err, errLiveMemoryRollbackUnproven) {
				a.poisonCurrentLiveMemoryWrites()
			}
			return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), err
		}
		delete(a.runtimePatchPatchLeases, feature.ID)
		a.runtimePatchPatchOrder = removeRuntimePatchPatchOrderID(a.runtimePatchPatchOrder, feature.ID)
		if len(a.runtimePatchPatchLeases) == 0 {
			a.runtimePatchPatchLeases = nil
			a.runtimePatchPatchOrder = nil
		}
		return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), nil
	}

	if active {
		if err := validateRuntimePatchOwnedLease(lease, token, process); err != nil {
			return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), err
		}
		status := a.runtimePatchStatusForFeatureLocked(*feature, token, memory)
		if lease.State == runtimePatchPatchEnabled && status.Enabled {
			return status, nil
		}
		lease.State = runtimePatchPatchRecovery
		a.runtimePatchPatchLeases[feature.ID] = cloneRuntimePatchPatchLease(lease)
		a.poisonCurrentLiveMemoryWrites()
		return status, errors.Join(fmt.Errorf("RuntimePatch feature requires recovery before it can be enabled again"), errLiveMemoryRollbackUnproven)
	}
	for activeID, activeLease := range a.runtimePatchPatchLeases {
		if !runtimeOwnerTokenMatches(activeLease.OwnerToken, token) {
			return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), fmt.Errorf("RuntimePatch feature %s is owned by another page", activeID)
		}
		if !sameProcessInstance(activeLease.Process, process) {
			return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), fmt.Errorf("RuntimePatch feature %s belongs to a replaced game process", activeID)
		}
		if activeLease.State != runtimePatchPatchEnabled {
			return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), fmt.Errorf("RuntimePatch feature %s requires recovery before another feature can be enabled", activeID)
		}
	}
	if conflict := findRuntimePatchCatalogConflict(*feature, a.runtimePatchPatchLeases); conflict != "" {
		return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), fmt.Errorf("RuntimePatch feature conflicts with active feature %s", conflict)
	}

	sites, err := a.prepareRuntimePatchSitesLocked(*feature, memory)
	if err != nil {
		return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), err
	}
	if overlap := findRuntimePatchActiveAddressOverlap(sites, a.runtimePatchPatchLeases, feature.ID); overlap != "" {
		return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), fmt.Errorf("RuntimePatch feature overlaps active feature %s", overlap)
	}
	if a.runtimePatchPatchLeases == nil {
		a.runtimePatchPatchLeases = make(map[string]runtimePatchPatchLease)
	}
	candidate := runtimePatchPatchLease{
		FeatureID:  feature.ID,
		OwnerToken: token,
		Process:    process,
		State:      runtimePatchPatchRecovery,
		Sites:      sites,
	}
	a.runtimePatchPatchLeases[feature.ID] = cloneRuntimePatchPatchLease(candidate)
	a.runtimePatchPatchOrder = append(a.runtimePatchPatchOrder, feature.ID)
	if err := installRuntimePatchSites(memory, sites); err != nil {
		if errors.Is(err, errLiveMemoryRollbackUnproven) {
			a.poisonCurrentLiveMemoryWrites()
		} else {
			delete(a.runtimePatchPatchLeases, feature.ID)
			a.runtimePatchPatchOrder = removeRuntimePatchPatchOrderID(a.runtimePatchPatchOrder, feature.ID)
			if len(a.runtimePatchPatchLeases) == 0 {
				a.runtimePatchPatchLeases = nil
				a.runtimePatchPatchOrder = nil
			}
		}
		return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), err
	}
	candidate.State = runtimePatchPatchEnabled
	a.runtimePatchPatchLeases[feature.ID] = cloneRuntimePatchPatchLease(candidate)
	return a.runtimePatchStatusForFeatureLocked(*feature, token, memory), nil
}

// RuntimePatchReleaseOwned restores all patches owned by the presented Chara token.
// Poison never prevents this recovery path.
func (a *App) RuntimePatchReleaseOwned(token string) error {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if strings.TrimSpace(token) == "" {
		return nil
	}
	hasOwnedLease := false
	for _, lease := range a.runtimePatchPatchLeases {
		if lease.OwnerToken == token {
			hasOwnedLease = true
			break
		}
	}
	if !hasOwnedLease {
		return nil
	}
	if a.hProcess == 0 || !processHandleAlive(a.hProcess) {
		a.dropRuntimePatchPatchesForOwnerLocked(token)
		return nil
	}
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.restoreAllRuntimePatchPatchesLocked(token)
}
