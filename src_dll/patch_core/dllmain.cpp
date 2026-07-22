// dllmain.cpp : 定义 DLL 应用程序的入口点。
#include "pch.h"
#include <Windows.h>
#include <cstdint>
#include <cstdlib>
#include <libmem/libmem.h>
#include <cstdio>
#include <cstring>

// 0 = refill needed, 1 = waiting for OD to begin, 2 = OD active.
static LONG g_autoOverdrivePhase = 0;

struct DamageMeterState
{
    volatile LONG64 monsterDamage;
};

static HANDLE g_damageMeterMapping = nullptr;
static DamageMeterState* g_damageMeter = nullptr;
static const wchar_t* kDamageMeterName = L"Local\\GBFRPlayerInfoEditDamageMeterV4";

static constexpr size_t kPlayerPointerCount = 8;

struct PlayerPointerState
{
    volatile LONG index;
    volatile LONG damageEnabled;
    float damageScale;
    uint32_t padding;
    uintptr_t pointers[kPlayerPointerCount];
};

static HANDLE g_playerPointerMapping = nullptr;
static PlayerPointerState* g_playerPointerState = nullptr;
static const wchar_t* kPlayerPointerName = L"Local\\GBFRPlayerInfoEditPlayerPointersV1";
static bool g_playerPointerHookInstalled = false;

static void InitDamageMeter()
{
    if (g_damageMeter) return;

    g_damageMeterMapping = CreateFileMappingW(INVALID_HANDLE_VALUE, nullptr, PAGE_READWRITE, 0, sizeof(DamageMeterState), kDamageMeterName);
    if (!g_damageMeterMapping) return;

    g_damageMeter = reinterpret_cast<DamageMeterState*>(MapViewOfFile(g_damageMeterMapping, FILE_MAP_ALL_ACCESS, 0, 0, sizeof(DamageMeterState)));
}

static void InitPlayerPointers()
{
    if (g_playerPointerState) return;

    g_playerPointerMapping = CreateFileMappingW(INVALID_HANDLE_VALUE, nullptr, PAGE_READWRITE, 0, sizeof(PlayerPointerState), kPlayerPointerName);
    if (!g_playerPointerMapping) return;

    g_playerPointerState = reinterpret_cast<PlayerPointerState*>(MapViewOfFile(g_playerPointerMapping, FILE_MAP_ALL_ACCESS, 0, 0, sizeof(PlayerPointerState)));
}

static void ClosePlayerPointers()
{
    if (g_playerPointerState)
    {
        UnmapViewOfFile(g_playerPointerState);
        g_playerPointerState = nullptr;
    }
    if (g_playerPointerMapping)
    {
        CloseHandle(g_playerPointerMapping);
        g_playerPointerMapping = nullptr;
    }
}

static void CloseDamageMeter()
{
    if (g_damageMeter)
    {
        UnmapViewOfFile(g_damageMeter);
        g_damageMeter = nullptr;
    }
    if (g_damageMeterMapping)
    {
        CloseHandle(g_damageMeterMapping);
        g_damageMeterMapping = nullptr;
    }
}

struct PatchPoint
{
    const char* id;
    const wchar_t* name;
    lm_address_t rva;
    const lm_byte_t* expected;
    lm_size_t size;
    const lm_byte_t* patch;
    bool hook;
};

static const lm_byte_t kLinkTimeExpected[] = { 0xC4, 0xC1, 0x7A, 0x11, 0x9C, 0x24, 0xB4, 0x01, 0x00, 0x00 };
static const lm_byte_t kLinkTimeDisablePatch[] = { 0xC4, 0xC1, 0x7A, 0x11, 0x84, 0x24, 0xB4, 0x01, 0x00, 0x00 };
static const lm_byte_t kNop10[] = { 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90 };

static const lm_byte_t kMonsterHpExpected[] = { 0x48, 0x8B, 0x41, 0x10, 0x45, 0x31, 0xC9 };
static const lm_byte_t kStunExpected[] = { 0xC5, 0xFA, 0x58, 0x86, 0x60, 0x08, 0x00, 0x00 };
// v1.3.2+: damage value is finalized in [rsi+0xD4] before this cap check.
static const lm_byte_t kMonsterDamageExpected[] = { 0x81, 0xBE, 0xD4, 0x00, 0x00, 0x00, 0x00, 0xE1, 0xF5, 0x05 };
static const lm_byte_t kInventorySet45Expected[] = { 0x41, 0x01, 0x76, 0x04, 0x4C, 0x89, 0xE1 };

static const lm_byte_t kPurpleExpected[] = { 0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x10, 0x0A, 0x00, 0x00 };
static const lm_byte_t kBlueGrowExpected[] = { 0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x20, 0x07, 0x00, 0x00 };
static const lm_byte_t kBlueDrainExpected[] = { 0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x70, 0x0A, 0x00, 0x00 };
static const lm_byte_t kOverdriveExpected[] = { 0x8B, 0x46, 0x10, 0x83, 0xF8, 0x03, 0x0F, 0x84, 0xC7, 0x00, 0x00, 0x00 };
static const lm_byte_t kNop9[] = { 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90 };

static const PatchPoint kMonsterPatches[] = {
    { "link_time_no_drain", L"link time no drain", 0x187228, kLinkTimeExpected, sizeof(kLinkTimeExpected), kNop10, false },
    { "link_time_disable", L"disable link time", 0x187228, kLinkTimeExpected, sizeof(kLinkTimeExpected), kLinkTimeDisablePatch, false },
    { "monster_hp", L"monster hp", 0x1F7A820, kMonsterHpExpected, sizeof(kMonsterHpExpected), nullptr, true },
    { "monster_damage", L"monster damage", 0x1FBDEB4, kMonsterDamageExpected, sizeof(kMonsterDamageExpected), nullptr, true },
    { "monster_stun", L"monster stun", 0xB29128, kStunExpected, sizeof(kStunExpected), nullptr, true },
    { "overdrive_state", L"overdrive state", 0x22CB316, kOverdriveExpected, sizeof(kOverdriveExpected), nullptr, true },
    { "inventory_set_45", L"inventory set 45", 0x356621, kInventorySet45Expected, sizeof(kInventorySet45Expected), nullptr, true },
    { "purple_drain", L"purple bar drain", 0xA0379A, kPurpleExpected, sizeof(kPurpleExpected), kNop9, false },
    { "blue_grow", L"blue bar grow", 0xA09AF1, kBlueGrowExpected, sizeof(kBlueGrowExpected), kNop9, false },
    { "blue_drain", L"blue bar drain", 0xA03F38, kBlueDrainExpected, sizeof(kBlueDrainExpected), kNop9, false },
};

static bool BytesEqual(const lm_byte_t* a, const lm_byte_t* b, lm_size_t size)
{
    for (lm_size_t i = 0; i < size; ++i)
    {
        if (a[i] != b[i]) return false;
    }
    return true;
}

static bool PatchBytes(lm_address_t target, const lm_byte_t* patch, lm_size_t size)
{
    lm_prot_t oldProt{};
    if (!LM_ProtMemory(target, size, LM_PROT_XRW, &oldProt)) return false;

    bool ok = LM_WriteMemory(target, patch, size) == size;
    LM_ProtMemory(target, size, oldProt, nullptr);
    FlushInstructionCache(GetCurrentProcess(), reinterpret_cast<void*>(target), size);
    return ok;
}

static void WritePatchDebugLog(const wchar_t* message)
{
    wchar_t tempPath[MAX_PATH]{};
    if (!GetTempPathW(_countof(tempPath), tempPath)) return;

    wchar_t path[MAX_PATH]{};
    swprintf_s(path, L"%sgbfr-player-info-edit\\patch_core_debug.txt", tempPath);
    HANDLE file = CreateFileW(path, GENERIC_WRITE, FILE_SHARE_READ, nullptr, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) return;

    DWORD written = 0;
    WriteFile(file, message, static_cast<DWORD>(wcslen(message) * sizeof(wchar_t)), &written, nullptr);
    CloseHandle(file);
}

static bool ReadPatchId(char* patchId, DWORD patchIdSize)
{
    wchar_t tempPath[MAX_PATH]{};
    if (!GetTempPathW(_countof(tempPath), tempPath)) return false;

    wchar_t commandPath[MAX_PATH]{};
    swprintf_s(commandPath, L"%sgbfr-player-info-edit\\patch_core_command.txt", tempPath);

    HANDLE file = CreateFileW(commandPath, GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, nullptr, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) return false;

    DWORD read = 0;
    BOOL ok = ReadFile(file, patchId, patchIdSize - 1, &read, nullptr);
    CloseHandle(file);
    if (!ok || read == 0) return false;

    patchId[read] = '\0';
    for (DWORD i = 0; i < read; ++i)
    {
        if (patchId[i] == '\r' || patchId[i] == '\n')
        {
            patchId[i] = '\0';
            break;
        }
    }
    return patchId[0] != '\0';
}

static bool PatchIdEquals(const char* requestedId, const char* pointId)
{
    size_t n = strlen(pointId);
    return strncmp(requestedId, pointId, n) == 0 && (requestedId[n] == '\0' || requestedId[n] == ' ' || requestedId[n] == '\t');
}

static int ReadIntValue(int defaultValue)
{
    char patchId[64]{};
    if (!ReadPatchId(patchId, sizeof(patchId))) return defaultValue;

    char* space = strchr(patchId, ' ');
    if (!space) return defaultValue;
    return atoi(space + 1);
}

static float ReadScale()
{
    char patchId[64]{};
    if (!ReadPatchId(patchId, sizeof(patchId))) return 1.0f;

    char* space = strchr(patchId, ' ');
    if (!space) return 1.0f;
    float scale = static_cast<float>(atof(space + 1));
    if (scale <= 0.0f || scale > 1000.0f) return 1.0f;
    return scale;
}

static void ConfigurePlayerDamage()
{
    if (!g_playerPointerState) return;

    char patchId[64]{};
    if (!ReadPatchId(patchId, sizeof(patchId))) return;
    if (!PatchIdEquals(patchId, "monster_damage")) return;

    char* space = strchr(patchId, ' ');
    float scale = space ? static_cast<float>(atof(space + 1)) : 0.0f;
    g_playerPointerState->damageEnabled = scale > 0.0f && scale <= 1000.0f;
    if (g_playerPointerState->damageEnabled) g_playerPointerState->damageScale = scale;
}

static lm_address_t AllocNear(lm_address_t target, size_t size)
{
    const uintptr_t granularity = 0x10000;
    const uintptr_t maxDistance = 0x7FFF0000;
    uintptr_t base = target & ~(granularity - 1);

    for (uintptr_t step = 0; step <= maxDistance; step += granularity)
    {
        uintptr_t candidates[2]{};
        int count = 0;
        if (base >= step) candidates[count++] = base - step;
        if (base <= UINTPTR_MAX - step) candidates[count++] = base + step;

        for (int i = 0; i < count; ++i)
        {
            void* ptr = VirtualAlloc(reinterpret_cast<void*>(candidates[i]), size, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE);
            if (!ptr) continue;

            int64_t delta = static_cast<int64_t>(reinterpret_cast<uintptr_t>(ptr)) - static_cast<int64_t>(target + 5);
            if (delta >= INT32_MIN && delta <= INT32_MAX)
            {
                return reinterpret_cast<lm_address_t>(ptr);
            }
            VirtualFree(ptr, 0, MEM_RELEASE);
        }
    }

    return LM_ADDRESS_BAD;
}

static void AppendTeamDamageFromRcXEdxR8(lm_byte_t* code, size_t& i, uint8_t damageOffset)
{
    code[i++] = 0x4C; code[i++] = 0x8B; code[i++] = 0x49; code[i++] = 0x10;                         // mov r9,[rcx+10]
    code[i++] = 0x4D; code[i++] = 0x85; code[i++] = 0xC9;                                           // test r9,r9
    code[i++] = 0x7E; size_t jleSkipOldHp = i++;                                                     // jle skip
    code[i++] = 0x4D; code[i++] = 0x89; code[i++] = 0xCA;                                           // mov r10,r9
    code[i++] = 0x49; code[i++] = 0x29; code[i++] = 0xD2;                                           // sub r10,rdx
    code[i++] = 0x45; code[i++] = 0x85; code[i++] = 0xC0;                                           // test r8d,r8d
    code[i++] = 0x74; size_t jzAllowZero = i++;                                                      // jz allow zero
    code[i++] = 0x49; code[i++] = 0x83; code[i++] = 0xFA; code[i++] = 0x01;                         // cmp r10,1
    code[i++] = 0x7D; size_t jgeHaveRemaining = i++;                                                 // jge have remaining
    code[i++] = 0x49; code[i++] = 0xBA; code[i++] = 0x01; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; // mov r10,1
    code[i++] = 0xEB; size_t jmpHaveRemaining = i++;                                                 // jmp have remaining
    size_t allowZeroOffset = i;
    code[i++] = 0x4D; code[i++] = 0x85; code[i++] = 0xD2;                                           // test r10,r10
    code[i++] = 0x7F; size_t jgHaveRemaining = i++;                                                  // jg have remaining
    code[i++] = 0x4D; code[i++] = 0x31; code[i++] = 0xD2;                                           // xor r10,r10
    size_t haveRemainingOffset = i;
    code[i++] = 0x4D; code[i++] = 0x29; code[i++] = 0xD1;                                           // sub r9,r10
    code[i++] = 0x7E; size_t jleSkipDelta = i++;                                                     // jle skip
    code[i++] = 0x49; code[i++] = 0xBB; uintptr_t meterAddr = reinterpret_cast<uintptr_t>(&g_damageMeter); memcpy(code + i, &meterAddr, sizeof(meterAddr)); i += sizeof(meterAddr); // mov r11,&g_damageMeter
    code[i++] = 0x4D; code[i++] = 0x8B; code[i++] = 0x1B;                                           // mov r11,[r11]
    code[i++] = 0x4D; code[i++] = 0x85; code[i++] = 0xDB;                                           // test r11,r11
    code[i++] = 0x74; size_t jzSkipMeter = i++;                                                      // jz skip
    code[i++] = 0xF0; code[i++] = 0x4D; code[i++] = 0x01; code[i++] = 0x4B; code[i++] = damageOffset;              // lock add [r11+damageOffset],r9
    size_t skipOffset = i;

    code[jleSkipOldHp] = static_cast<lm_byte_t>(skipOffset - (jleSkipOldHp + 1));
    code[jzAllowZero] = static_cast<lm_byte_t>(allowZeroOffset - (jzAllowZero + 1));
    code[jgeHaveRemaining] = static_cast<lm_byte_t>(haveRemainingOffset - (jgeHaveRemaining + 1));
    code[jmpHaveRemaining] = static_cast<lm_byte_t>(haveRemainingOffset - (jmpHaveRemaining + 1));
    code[jgHaveRemaining] = static_cast<lm_byte_t>(haveRemainingOffset - (jgHaveRemaining + 1));
    code[jleSkipDelta] = static_cast<lm_byte_t>(skipOffset - (jleSkipDelta + 1));
    code[jzSkipMeter] = static_cast<lm_byte_t>(skipOffset - (jzSkipMeter + 1));
}

static void AppendTeamDamageFromRsiEdi(lm_byte_t* code, size_t& i, uint8_t damageOffset)
{
    code[i++] = 0x44; code[i++] = 0x8B; code[i++] = 0x4E; code[i++] = 0x10;                         // mov r9d,[rsi+10]
    code[i++] = 0x45; code[i++] = 0x85; code[i++] = 0xC9;                                           // test r9d,r9d
    code[i++] = 0x7E; size_t jleSkipOldHp = i++;                                                     // jle skip
    code[i++] = 0x45; code[i++] = 0x89; code[i++] = 0xCA;                                           // mov r10d,r9d
    code[i++] = 0x41; code[i++] = 0x29; code[i++] = 0xFA;                                           // sub r10d,edi
    code[i++] = 0x45; code[i++] = 0x85; code[i++] = 0xD2;                                           // test r10d,r10d
    code[i++] = 0x7F; size_t jgHaveRemaining = i++;                                                  // jg have remaining
    code[i++] = 0x45; code[i++] = 0x31; code[i++] = 0xD2;                                           // xor r10d,r10d
    size_t haveRemainingOffset = i;
    code[i++] = 0x45; code[i++] = 0x29; code[i++] = 0xD1;                                           // sub r9d,r10d
    code[i++] = 0x7E; size_t jleSkipDelta = i++;                                                     // jle skip
    code[i++] = 0x49; code[i++] = 0xBB; uintptr_t meterAddr = reinterpret_cast<uintptr_t>(&g_damageMeter); memcpy(code + i, &meterAddr, sizeof(meterAddr)); i += sizeof(meterAddr); // mov r11,&g_damageMeter
    code[i++] = 0x4D; code[i++] = 0x8B; code[i++] = 0x1B;                                           // mov r11,[r11]
    code[i++] = 0x4D; code[i++] = 0x85; code[i++] = 0xDB;                                           // test r11,r11
    code[i++] = 0x74; size_t jzSkipMeter = i++;                                                      // jz skip
    code[i++] = 0xF0; code[i++] = 0x4D; code[i++] = 0x01; code[i++] = 0x4B; code[i++] = damageOffset;              // lock add [r11+damageOffset],r9
    size_t skipOffset = i;

    code[jleSkipOldHp] = static_cast<lm_byte_t>(skipOffset - (jleSkipOldHp + 1));
    code[jgHaveRemaining] = static_cast<lm_byte_t>(haveRemainingOffset - (jgHaveRemaining + 1));
    code[jleSkipDelta] = static_cast<lm_byte_t>(skipOffset - (jleSkipDelta + 1));
    code[jzSkipMeter] = static_cast<lm_byte_t>(skipOffset - (jzSkipMeter + 1));
}

static bool PatchDamageHook(lm_address_t target, wchar_t* message, size_t messageSize)
{
    float scale = ReadScale();
    lm_address_t cave = AllocNear(target, 256);
    if (cave == LM_ADDRESS_BAD)
    {
        swprintf_s(message, messageSize, L"alloc near failed: monster hp");
        return false;
    }

    lm_byte_t code[192]{};
    size_t i = 0;
    code[i++] = 0x41; code[i++] = 0x53;                                                             // push r11
    code[i++] = 0x41; code[i++] = 0x52;                                                             // push r10
    code[i++] = 0x48; code[i++] = 0x83; code[i++] = 0xEC; code[i++] = 0x10;                         // sub rsp,10
    code[i++] = 0x0F; code[i++] = 0x11; code[i++] = 0x04; code[i++] = 0x24;                         // movups [rsp],xmm0
    code[i++] = 0xF3; code[i++] = 0x0F; code[i++] = 0x2A; code[i++] = 0xC2;                         // cvtsi2ss xmm0,edx
    code[i++] = 0xF3; code[i++] = 0x0F; code[i++] = 0x59; code[i++] = 0x05;                         // mulss xmm0,[rip+disp32]
    size_t scaleDisp = i; i += 4;
    code[i++] = 0xF3; code[i++] = 0x0F; code[i++] = 0x2C; code[i++] = 0xD0;                         // cvttss2si edx,xmm0
    code[i++] = 0x85; code[i++] = 0xD2;                                                             // test edx,edx
    code[i++] = 0x7F; size_t jgScaled = i++;                                                        // jg scaled
    code[i++] = 0xBA; code[i++] = 0x01; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00;       // mov edx,1
    size_t scaledOffset = i;
    code[i++] = 0x0F; code[i++] = 0x10; code[i++] = 0x04; code[i++] = 0x24;                         // movups xmm0,[rsp]
    code[i++] = 0x48; code[i++] = 0x83; code[i++] = 0xC4; code[i++] = 0x10;                         // add rsp,10
    AppendTeamDamageFromRcXEdxR8(code, i, 0);
    code[i++] = 0x41; code[i++] = 0x5A;                                                             // pop r10
    code[i++] = 0x41; code[i++] = 0x5B;                                                             // pop r11
    code[i++] = 0x48; code[i++] = 0x8B; code[i++] = 0x41; code[i++] = 0x10;                         // mov rax,[rcx+10]
    code[i++] = 0x45; code[i++] = 0x31; code[i++] = 0xC9;                                           // xor r9d,r9d
    code[i++] = 0xE9;                                                                               // jmp return
    size_t jmpBackDisp = i; i += 4;
    size_t scaleOffset = i;
    memcpy(code + i, &scale, sizeof(scale)); i += sizeof(scale);

    code[jgScaled] = static_cast<lm_byte_t>(scaledOffset - (jgScaled + 1));

    int64_t scaleDelta = static_cast<int64_t>(cave + scaleOffset) - static_cast<int64_t>(cave + scaleDisp + 4);
    int64_t backDelta = static_cast<int64_t>(target + 7) - static_cast<int64_t>(cave + jmpBackDisp + 4);
    if (scaleDelta < INT32_MIN || scaleDelta > INT32_MAX || backDelta < INT32_MIN || backDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"jump out of range: monster hp");
        return false;
    }
    int32_t relScale = static_cast<int32_t>(scaleDelta);
    int32_t relBack = static_cast<int32_t>(backDelta);
    memcpy(code + scaleDisp, &relScale, sizeof(relScale));
    memcpy(code + jmpBackDisp, &relBack, sizeof(relBack));

    if (LM_WriteMemory(cave, code, i) != i)
    {
        swprintf_s(message, messageSize, L"cave write failed: monster hp");
        return false;
    }

    lm_byte_t jmp[7]{ 0xE9 };
    memset(jmp + 5, 0x90, sizeof(jmp) - 5);
    int32_t rel = static_cast<int32_t>(cave - (target + 5));
    memcpy(jmp + 1, &rel, sizeof(rel));
    if (!PatchBytes(target, jmp, sizeof(jmp)))
    {
        swprintf_s(message, messageSize, L"hook write failed: monster hp");
        return false;
    }
    return true;
}

static bool InstallPlayerPointerHook(const lm_module_t& module, wchar_t* message, size_t messageSize)
{
    if (g_playerPointerHookInstalled) return true;

    lm_address_t match = LM_SigScan("FF 90 ?? ?? 00 00 ?? ?? ?? ?? ?? ?? ?? ?? 8B ?? ?? ?? 00 00 48 81 C1 ?? ?? 00 00 FF ?? ?? ?? 00 00 ?? 39", module.base, module.size);
    if (match == LM_ADDRESS_BAD)
    {
        swprintf_s(message, messageSize, L"player pointer signature not found");
        return false;
    }

    lm_address_t target = match + 0x14;
    lm_byte_t original[7]{};
    if (LM_ReadMemory(target, original, sizeof(original)) != sizeof(original))
    {
        swprintf_s(message, messageSize, L"read failed: player pointer instruction");
        return false;
    }
    if (original[0] == 0xE9)
    {
        // Earlier injected DLL owns this permanent pointer-capture hook.
        g_playerPointerHookInstalled = true;
        return true;
    }
    if (original[0] != 0x48 || original[1] != 0x81 || original[2] != 0xC1)
    {
        swprintf_s(message, messageSize, L"unexpected player pointer instruction");
        return false;
    }

    lm_address_t cave = AllocNear(target, 96);
    if (cave == LM_ADDRESS_BAD)
    {
        swprintf_s(message, messageSize, L"alloc near failed: player pointers");
        return false;
    }

    lm_byte_t code[96]{};
    size_t i = 0;
    code[i++] = 0x50;                                                                               // push rax
    code[i++] = 0x52;                                                                               // push rdx
    code[i++] = 0x48; code[i++] = 0xBA; uintptr_t stateAddr = reinterpret_cast<uintptr_t>(g_playerPointerState); memcpy(code + i, &stateAddr, sizeof(stateAddr)); i += sizeof(stateAddr); // mov rdx,PlayerPointerState*
    code[i++] = 0x48; code[i++] = 0x85; code[i++] = 0xD2;                                           // test rdx,rdx
    code[i++] = 0x74; size_t jzSkipCapture = i++;                                                    // je skip capture
    code[i++] = 0x8B; code[i++] = 0x02;                                                             // mov eax,[rdx]
    code[i++] = 0xFF; code[i++] = 0x02;                                                             // inc dword ptr [rdx]
    code[i++] = 0x83; code[i++] = 0xE0; code[i++] = 0x07;                                           // and eax,7
    code[i++] = 0x48; code[i++] = 0x89; code[i++] = 0x4C; code[i++] = 0xC2; code[i++] = 0x10;       // mov [rdx+rax*8+10],rcx
    size_t skipCaptureOffset = i;
    code[jzSkipCapture] = static_cast<lm_byte_t>(skipCaptureOffset - (jzSkipCapture + 1));
    code[i++] = 0x5A;                                                                               // pop rdx
    code[i++] = 0x58;                                                                               // pop rax
    memcpy(code + i, original, sizeof(original)); i += sizeof(original);
    code[i++] = 0xE9;
    size_t returnDisp = i; i += 4;

    int64_t backDelta = static_cast<int64_t>(target + sizeof(original)) - static_cast<int64_t>(cave + returnDisp + 4);
    if (backDelta < INT32_MIN || backDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"return jump out of range: player pointers");
        return false;
    }
    int32_t relBack = static_cast<int32_t>(backDelta);
    memcpy(code + returnDisp, &relBack, sizeof(relBack));

    if (LM_WriteMemory(cave, code, i) != i)
    {
        swprintf_s(message, messageSize, L"cave write failed: player pointers");
        return false;
    }

    lm_byte_t jump[7]{ 0xE9 };
    memset(jump + 5, 0x90, sizeof(jump) - 5);
    int64_t hookDelta = static_cast<int64_t>(cave) - static_cast<int64_t>(target + 5);
    if (hookDelta < INT32_MIN || hookDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"hook jump out of range: player pointers");
        return false;
    }
    int32_t rel = static_cast<int32_t>(hookDelta);
    memcpy(jump + 1, &rel, sizeof(rel));
    if (!PatchBytes(target, jump, sizeof(jump)))
    {
        swprintf_s(message, messageSize, L"hook write failed: player pointers");
        return false;
    }

    g_playerPointerHookInstalled = true;
    return true;
}

static bool PatchMonsterDamageHook(lm_address_t target, wchar_t* message, size_t messageSize)
{
    lm_address_t cave = AllocNear(target, 192);
    if (cave == LM_ADDRESS_BAD)
    {
        swprintf_s(message, messageSize, L"alloc near failed: monster damage");
        return false;
    }

    lm_byte_t code[256]{};
    size_t i = 0;
    size_t playerJumps[kPlayerPointerCount]{};
    code[i++] = 0x50;                                                                               // push rax
    code[i++] = 0x52;                                                                               // push rdx
    code[i++] = 0x48; code[i++] = 0xBA; uintptr_t stateAddr = reinterpret_cast<uintptr_t>(g_playerPointerState); memcpy(code + i, &stateAddr, sizeof(stateAddr)); i += sizeof(stateAddr); // mov rdx,PlayerPointerState*
    code[i++] = 0x48; code[i++] = 0x85; code[i++] = 0xD2;                                           // test rdx,rdx
    code[i++] = 0x74; size_t jzSkipPlayerCheck = i++;                                                // je skip player check
    code[i++] = 0x83; code[i++] = 0x7A; code[i++] = 0x04; code[i++] = 0x00;                         // cmp dword ptr [rdx+4],0
    code[i++] = 0x74; size_t jzDisabled = i++;                                                       // je skip player check
    for (size_t player = 0; player < kPlayerPointerCount; ++player)
    {
        code[i++] = 0x4C; code[i++] = 0x3B; code[i++] = 0x72; code[i++] = static_cast<lm_byte_t>(0x10 + player * 8); // cmp r14,[rdx+player]
        code[i++] = 0x74; playerJumps[player] = i++;                                                // je scale player damage
    }
    size_t skipPlayerCheckOffset = i;
    code[jzSkipPlayerCheck] = static_cast<lm_byte_t>(skipPlayerCheckOffset - (jzSkipPlayerCheck + 1));
    code[jzDisabled] = static_cast<lm_byte_t>(skipPlayerCheckOffset - (jzDisabled + 1));
    code[i++] = 0x5A;                                                                               // pop rdx
    code[i++] = 0x58;                                                                               // pop rax
    code[i++] = 0xEB; size_t jmpSkipScale = i++;                                                    // jmp compare
    size_t scalePlayerDamageOffset = i;
    code[i++] = 0x48; code[i++] = 0x83; code[i++] = 0xEC; code[i++] = 0x10;                         // sub rsp,10
    code[i++] = 0x0F; code[i++] = 0x11; code[i++] = 0x04; code[i++] = 0x24;                         // movups [rsp],xmm0
    code[i++] = 0x8B; code[i++] = 0x86; code[i++] = 0xD4; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; // mov eax,[rsi+D4]
    code[i++] = 0xF3; code[i++] = 0x0F; code[i++] = 0x2A; code[i++] = 0xC0;                         // cvtsi2ss xmm0,eax
    code[i++] = 0xF3; code[i++] = 0x0F; code[i++] = 0x59; code[i++] = 0x42; code[i++] = 0x08;       // mulss xmm0,[rdx+8]
    code[i++] = 0xF3; code[i++] = 0x0F; code[i++] = 0x2C; code[i++] = 0xC0;                         // cvttss2si eax,xmm0
    code[i++] = 0x85; code[i++] = 0xC0;                                                             // test eax,eax
    code[i++] = 0x7F; size_t jgScaled = i++;                                                        // jg scaled
    code[i++] = 0xB8; code[i++] = 0x01; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00;       // mov eax,1
    size_t scaledOffset = i;
    code[i++] = 0x89; code[i++] = 0x86; code[i++] = 0xD4; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; // mov [rsi+D4],eax
    code[i++] = 0x0F; code[i++] = 0x10; code[i++] = 0x04; code[i++] = 0x24;                         // movups xmm0,[rsp]
    code[i++] = 0x48; code[i++] = 0x83; code[i++] = 0xC4; code[i++] = 0x10;                         // add rsp,10
    size_t restoreOffset = i;
    code[i++] = 0x5A;                                                                               // pop rdx
    code[i++] = 0x58;                                                                               // pop rax
    size_t compareOffset = i;
    code[jmpSkipScale] = static_cast<lm_byte_t>(compareOffset - (jmpSkipScale + 1));
    for (size_t player = 0; player < kPlayerPointerCount; ++player)
    {
        code[playerJumps[player]] = static_cast<lm_byte_t>(scalePlayerDamageOffset - (playerJumps[player] + 1));
    }
    code[i++] = 0x81; code[i++] = 0xBE; code[i++] = 0xD4; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; // cmp dword ptr [rsi+D4],05F5E100
    code[i++] = 0x00; code[i++] = 0xE1; code[i++] = 0xF5; code[i++] = 0x05;
    code[i++] = 0xE9;                                                                               // jmp return
    size_t jmpBackDisp = i; i += 4;

    code[jgScaled] = static_cast<lm_byte_t>(scaledOffset - (jgScaled + 1));

    int64_t backDelta = static_cast<int64_t>(target + sizeof(kMonsterDamageExpected)) - static_cast<int64_t>(cave + jmpBackDisp + 4);
    if (backDelta < INT32_MIN || backDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"return jump out of range: monster damage");
        return false;
    }
    int32_t relBack = static_cast<int32_t>(backDelta);
    memcpy(code + jmpBackDisp, &relBack, sizeof(relBack));

    if (LM_WriteMemory(cave, code, i) != i)
    {
        swprintf_s(message, messageSize, L"cave write failed: monster damage");
        return false;
    }

    lm_byte_t jmp[sizeof(kMonsterDamageExpected)]{ 0xE9 };
    memset(jmp + 5, 0x90, sizeof(jmp) - 5);
    int64_t hookDelta = static_cast<int64_t>(cave) - static_cast<int64_t>(target + 5);
    if (hookDelta < INT32_MIN || hookDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"hook jump out of range: monster damage");
        return false;
    }
    int32_t rel = static_cast<int32_t>(hookDelta);
    memcpy(jmp + 1, &rel, sizeof(rel));
    if (!PatchBytes(target, jmp, sizeof(jmp)))
    {
        swprintf_s(message, messageSize, L"hook write failed: monster damage");
        return false;
    }
    return true;
}

static bool PatchOverdriveHook(lm_address_t target, wchar_t* message, size_t messageSize)
{
    // sub_1422CB250 reads ModeState.state once per UI update. Overwrite it
    // before that read so changing this option immediately updates the gauge.
    int requested = ReadIntValue(3);
    bool autoMode = requested == 9;
    if (requested != 0 && requested != 3 && !autoMode) requested = 3;

    lm_address_t cave = AllocNear(target, 128);
    if (cave == LM_ADDRESS_BAD)
    {
        swprintf_s(message, messageSize, L"alloc near failed: overdrive state");
        return false;
    }

    lm_byte_t code[96]{};
    size_t i = 0;
    if (autoMode)
    {
        // Phase 1 blocks refills during OD startup. Only phase 2 (state 2 was
        // observed) may refill again after state 2 ends.
        code[i++] = 0x8B; code[i++] = 0x46; code[i++] = 0x10; // mov eax,[rsi+10]
        code[i++] = 0x49; code[i++] = 0xBB; // mov r11,&g_autoOverdrivePhase
        uintptr_t phaseAddr = reinterpret_cast<uintptr_t>(&g_autoOverdrivePhase);
        memcpy(code + i, &phaseAddr, sizeof(phaseAddr)); i += sizeof(phaseAddr);
        code[i++] = 0x83; code[i++] = 0xF8; code[i++] = 0x02; // cmp eax,2
        code[i++] = 0x75; size_t jneNotActive = i++;          // jne not active
        code[i++] = 0x41; code[i++] = 0xC7; code[i++] = 0x03; code[i++] = 0x02; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; // mov dword [r11],2
        code[i++] = 0xEB; size_t jmpReadActive = i++; // jmp read
        size_t notActiveOffset = i;
        code[i++] = 0x41; code[i++] = 0x83; code[i++] = 0x3B; code[i++] = 0x02; // cmp dword [r11],2
        code[i++] = 0x74; size_t jeRefill = i++; // je refill
        code[i++] = 0x41; code[i++] = 0x83; code[i++] = 0x3B; code[i++] = 0x00; // cmp dword [r11],0
        code[i++] = 0x75; size_t jneReadWaiting = i++; // jne read
        size_t refillOffset = i;
        code[i++] = 0xC7; code[i++] = 0x46; code[i++] = 0x10; code[i++] = 0x03; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; // mov [rsi+10],3
        code[i++] = 0x41; code[i++] = 0xC7; code[i++] = 0x03; code[i++] = 0x01; code[i++] = 0x00; code[i++] = 0x00; code[i++] = 0x00; // mov dword [r11],1
        size_t readOffset = i;
        code[jneNotActive] = static_cast<lm_byte_t>(notActiveOffset - (jneNotActive + 1));
        code[jmpReadActive] = static_cast<lm_byte_t>(readOffset - (jmpReadActive + 1));
        code[jeRefill] = static_cast<lm_byte_t>(refillOffset - (jeRefill + 1));
        code[jneReadWaiting] = static_cast<lm_byte_t>(readOffset - (jneReadWaiting + 1));
    }
    else
    {
        code[i++] = 0xC7; code[i++] = 0x46; code[i++] = 0x10; // mov dword ptr [rsi+10],state
        memcpy(code + i, &requested, sizeof(requested)); i += sizeof(requested);
    }
    code[i++] = 0x8B; code[i++] = 0x46; code[i++] = 0x10; // mov eax,[rsi+10]
    code[i++] = 0x83; code[i++] = 0xF8; code[i++] = 0x03; // cmp eax,3
    code[i++] = 0xE9;
    size_t backDisp = i; i += 4;

    int64_t backDelta = static_cast<int64_t>(target + 6) - static_cast<int64_t>(cave + backDisp + 4);
    if (backDelta < INT32_MIN || backDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"return jump out of range: overdrive state");
        return false;
    }
    int32_t backRel = static_cast<int32_t>(backDelta);
    memcpy(code + backDisp, &backRel, sizeof(backRel));

    if (LM_WriteMemory(cave, code, i) != i)
    {
        swprintf_s(message, messageSize, L"cave write failed: overdrive state");
        return false;
    }

    lm_byte_t jmp[sizeof(kOverdriveExpected)]{ 0xE9 };
    memset(jmp + 5, 0x90, sizeof(jmp) - 5);
    int64_t hookDelta = static_cast<int64_t>(cave) - static_cast<int64_t>(target + 5);
    if (hookDelta < INT32_MIN || hookDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"hook jump out of range: overdrive state");
        return false;
    }
    int32_t hookRel = static_cast<int32_t>(hookDelta);
    memcpy(jmp + 1, &hookRel, sizeof(hookRel));
    if (!PatchBytes(target, jmp, sizeof(jmp)))
    {
        swprintf_s(message, messageSize, L"hook write failed: overdrive state");
        return false;
    }
    return true;
}

static bool PatchInventorySetQuantityHook(lm_address_t target, wchar_t* message, size_t messageSize)
{
    int quantity = ReadIntValue(45);
    if (quantity < 1 || quantity > 9999) quantity = 45;

    lm_address_t cave = AllocNear(target, 32);
    if (cave == LM_ADDRESS_BAD)
    {
        swprintf_s(message, messageSize, L"alloc near failed: inventory quantity");
        return false;
    }

    lm_byte_t code[24]{};
    size_t i = 0;
    code[i++] = 0x41; code[i++] = 0xC7; code[i++] = 0x46; code[i++] = 0x04;                         // mov dword ptr [r14+04],quantity
    memcpy(code + i, &quantity, sizeof(quantity)); i += sizeof(quantity);
    code[i++] = 0x4C; code[i++] = 0x89; code[i++] = 0xE1;                                           // mov rcx,r12
    code[i++] = 0xE9;
    size_t jmpBackDisp = i; i += 4;

    int64_t backDelta = static_cast<int64_t>(target + 7) - static_cast<int64_t>(cave + jmpBackDisp + 4);
    if (backDelta < INT32_MIN || backDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"return jump out of range: inventory quantity");
        return false;
    }
    int32_t relBack = static_cast<int32_t>(backDelta);
    memcpy(code + jmpBackDisp, &relBack, sizeof(relBack));

    if (LM_WriteMemory(cave, code, i) != i)
    {
        swprintf_s(message, messageSize, L"cave write failed: inventory quantity");
        return false;
    }

    lm_byte_t jmp[7]{ 0xE9 };
    memset(jmp + 5, 0x90, sizeof(jmp) - 5);
    int64_t hookDelta = static_cast<int64_t>(cave) - static_cast<int64_t>(target + 5);
    if (hookDelta < INT32_MIN || hookDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"hook jump out of range: inventory quantity");
        return false;
    }
    int32_t relHook = static_cast<int32_t>(hookDelta);
    memcpy(jmp + 1, &relHook, sizeof(relHook));
    if (!PatchBytes(target, jmp, sizeof(jmp)))
    {
        swprintf_s(message, messageSize, L"hook write failed: inventory quantity");
        return false;
    }
    return true;
}

static bool PatchStunHook(lm_address_t target, wchar_t* message, size_t messageSize)
{
    float scale = ReadScale();
    lm_address_t cave = AllocNear(target, 128);
    if (cave == LM_ADDRESS_BAD)
    {
        swprintf_s(message, messageSize, L"alloc near failed: monster stun");
        return false;
    }

    lm_byte_t code[64]{};
    size_t i = 0;
    code[i++] = 0x50;                                                                               // push rax
    code[i++] = 0x48; code[i++] = 0x8D; code[i++] = 0x86; code[i++] = 0x60; code[i++] = 0x08; code[i++] = 0x00; code[i++] = 0x00; // lea rax,[rsi+860]
    code[i++] = 0xF3; code[i++] = 0x0F; code[i++] = 0x59; code[i++] = 0x05;                         // mulss xmm0,[rip+disp32]
    size_t scaleDisp = i; i += 4;
    code[i++] = 0xC5; code[i++] = 0xFA; code[i++] = 0x58; code[i++] = 0x00;                         // vaddss xmm0,xmm0,[rax]
    code[i++] = 0x58;                                                                               // pop rax
    code[i++] = 0xE9;                                                                               // jmp return
    size_t jmpBackDisp = i; i += 4;
    size_t scaleOffset = i;
    memcpy(code + i, &scale, sizeof(scale)); i += sizeof(scale);

    int64_t scaleDelta = static_cast<int64_t>(cave + scaleOffset) - static_cast<int64_t>(cave + scaleDisp + 4);
    if (scaleDelta < INT32_MIN || scaleDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"scale jump out of range: monster stun");
        return false;
    }
    int32_t relScale = static_cast<int32_t>(scaleDelta);
    memcpy(code + scaleDisp, &relScale, sizeof(relScale));

    int64_t backDelta = static_cast<int64_t>(target + sizeof(kStunExpected)) - static_cast<int64_t>(cave + jmpBackDisp + 4);
    if (backDelta < INT32_MIN || backDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"return jump out of range: monster stun");
        return false;
    }
    int32_t relBack = static_cast<int32_t>(backDelta);
    memcpy(code + jmpBackDisp, &relBack, sizeof(relBack));

    if (LM_WriteMemory(cave, code, i) != i)
    {
        swprintf_s(message, messageSize, L"cave write failed: monster stun");
        return false;
    }

    lm_byte_t jmp[sizeof(kStunExpected)]{ 0xE9 };
    memset(jmp + 5, 0x90, sizeof(jmp) - 5);
    int64_t hookDelta = static_cast<int64_t>(cave) - static_cast<int64_t>(target + 5);
    if (hookDelta < INT32_MIN || hookDelta > INT32_MAX)
    {
        swprintf_s(message, messageSize, L"hook jump out of range: monster stun");
        return false;
    }
    int32_t rel = static_cast<int32_t>(hookDelta);
    memcpy(jmp + 1, &rel, sizeof(rel));
    if (!PatchBytes(target, jmp, sizeof(jmp)))
    {
        swprintf_s(message, messageSize, L"hook write failed: monster stun");
        return false;
    }
    return true;
}

static bool ShouldApply(const char* requestedId, const PatchPoint& point)
{
    return strcmp(requestedId, "all") == 0 || PatchIdEquals(requestedId, point.id);
}

static bool ApplyMonsterPatches(wchar_t* message, size_t messageSize)
{
    char patchId[64]{};
    if (!ReadPatchId(patchId, sizeof(patchId)))
    {
        strcpy_s(patchId, "all");
    }

    lm_module_t module{};
    if (!LM_FindModule("granblue_fantasy_relink.exe", &module))
    {
        swprintf_s(message, messageSize, L"LM_FindModule failed");
        return false;
    }

    if ((PatchIdEquals(patchId, "monster_damage") || strcmp(patchId, "all") == 0) && !InstallPlayerPointerHook(module, message, messageSize))
    {
        return false;
    }

    int patched = 0;
    int already = 0;
    int selected = 0;

    for (const auto& point : kMonsterPatches)
    {
        if (!ShouldApply(patchId, point)) continue;
        ++selected;

        lm_address_t target = module.base + point.rva;
        lm_byte_t current[16]{};
        if (point.size > sizeof(current) || LM_ReadMemory(target, current, point.size) != point.size)
        {
            swprintf_s(message, messageSize, L"read failed: %s at +%llX", point.name, static_cast<unsigned long long>(point.rva));
            return false;
        }

        if (point.hook && current[0] == 0xE9)
        {
            ++already;
            continue;
        }
        if (!point.hook && BytesEqual(current, point.patch, point.size))
        {
            ++already;
            continue;
        }

        if (!BytesEqual(current, point.expected, point.size))
        {
            swprintf_s(message, messageSize, L"unexpected bytes: %s at +%llX", point.name, static_cast<unsigned long long>(point.rva));
            return false;
        }

        if (point.hook)
        {
            if (strcmp(point.id, "monster_stun") == 0)
            {
                if (!PatchStunHook(target, message, messageSize)) return false;
            }
            else if (strcmp(point.id, "monster_damage") == 0)
            {
                if (!PatchMonsterDamageHook(target, message, messageSize)) return false;
            }
            else if (strcmp(point.id, "overdrive_state") == 0)
            {
                if (!PatchOverdriveHook(target, message, messageSize)) return false;
            }
            else if (strcmp(point.id, "inventory_set_45") == 0)
            {
                if (!PatchInventorySetQuantityHook(target, message, messageSize)) return false;
            }
            else if (!PatchDamageHook(target, message, messageSize)) return false;
        }
        else if (!PatchBytes(target, point.patch, point.size))
        {
            swprintf_s(message, messageSize, L"write failed: %s at +%llX", point.name, static_cast<unsigned long long>(point.rva));
            return false;
        }
        ++patched;
    }

    if (selected == 0)
    {
        swprintf_s(message, messageSize, L"unknown patch id: %S", patchId);
        return false;
    }

    swprintf_s(message, messageSize, L"monster enhance ok: id %S patched %d, already %d", patchId, patched, already);
    return true;
}

static DWORD WINAPI InitThread(LPVOID)
{
    InitDamageMeter();
    InitPlayerPointers();
    ConfigurePlayerDamage();

    wchar_t message[256]{};
    ApplyMonsterPatches(message, _countof(message));
    WritePatchDebugLog(message);

    wchar_t debugMessage[320]{};
    swprintf_s(debugMessage, L"[patch_core] %s\n", message);
    OutputDebugStringW(debugMessage);

    return 0;
}

BOOL APIENTRY DllMain( HMODULE hModule,
                       DWORD  ul_reason_for_call,
                       LPVOID lpReserved
                     )
{
    switch (ul_reason_for_call)
    {
    case DLL_PROCESS_ATTACH:
        DisableThreadLibraryCalls(hModule);
        if (HANDLE thread = CreateThread(nullptr, 0, InitThread, nullptr, 0, nullptr))
        {
            CloseHandle(thread);
        }
        break;
    case DLL_PROCESS_DETACH:
        CloseDamageMeter();
        break;
    }
    return TRUE;
}

