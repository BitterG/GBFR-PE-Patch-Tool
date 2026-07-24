package backend

import "fmt"

// The material-consumption NOP and the temporary inventory-quantity hook both
// target the same instruction. Treat that instruction as one owned patch site,
// never as two independent toggles.
type sharedRuntimePatchOwner string

const (
	sharedRuntimePatchOwnerNone              sharedRuntimePatchOwner = "none"
	sharedRuntimePatchOwnerMaterialConsume   sharedRuntimePatchOwner = "material-consume"
	sharedRuntimePatchOwnerInventoryQuantity sharedRuntimePatchOwner = "inventory-quantity"
	sharedRuntimePatchOwnerUnknown           sharedRuntimePatchOwner = "unknown"
)

var sharedInventoryMaterialOriginal = []byte{0x41, 0x01, 0x76, 0x04, 0x4C, 0x89, 0xE1}

func classifySharedRuntimePatch(current []byte) sharedRuntimePatchOwner {
	if len(current) != len(sharedInventoryMaterialOriginal) {
		return sharedRuntimePatchOwnerUnknown
	}
	if bytesEqual(current, sharedInventoryMaterialOriginal) {
		return sharedRuntimePatchOwnerNone
	}
	if bytesEqual(current[:len(materialConsumePatch)], materialConsumePatch) &&
		bytesEqual(current[len(materialConsumePatch):], sharedInventoryMaterialOriginal[len(materialConsumePatch):]) {
		return sharedRuntimePatchOwnerMaterialConsume
	}
	if current[0] == 0xE9 &&
		bytesEqual(current[5:], sharedInventoryMaterialOriginal[5:]) {
		return sharedRuntimePatchOwnerInventoryQuantity
	}
	return sharedRuntimePatchOwnerUnknown
}

func sharedRuntimePatchOwnerLabel(owner sharedRuntimePatchOwner) string {
	switch owner {
	case sharedRuntimePatchOwnerMaterialConsume:
		return "素材不消耗"
	case sharedRuntimePatchOwnerInventoryQuantity:
		return "小钳蟹数量钩子"
	case sharedRuntimePatchOwnerNone:
		return "原始指令"
	default:
		return "未知补丁"
	}
}

func validateSharedRuntimePatchTransition(current []byte, actor sharedRuntimePatchOwner, enabled bool) error {
	owner := classifySharedRuntimePatch(current)
	if owner == sharedRuntimePatchOwnerUnknown {
		return fmt.Errorf("共享补丁地址指令字节未知: %s", bytesToHex(current))
	}
	if actor != sharedRuntimePatchOwnerMaterialConsume && actor != sharedRuntimePatchOwnerInventoryQuantity {
		return fmt.Errorf("未知共享补丁所有者: %s", actor)
	}
	if owner != sharedRuntimePatchOwnerNone && owner != actor {
		return fmt.Errorf("共享补丁地址正由%s占用，请先恢复后再切换", sharedRuntimePatchOwnerLabel(owner))
	}
	// Re-applying the same state is harmless and keeps refresh/retry idempotent.
	_ = enabled
	return nil
}
