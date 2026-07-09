# x-cursor-checksum 算法（3.10.20 逆向完成）

## 完整公式

```
x-cursor-checksum = base64(obfuscate(6_byte_timestamp)) + machineId + '/' + macMachineId
```

## 分步说明

### 1. 时间戳（JS 特有的 32-bit shift 语义）

```js
E = Math.floor(Date.now() / 1e6)           // Date.now() 是毫秒，除以 1e6 秒后精度 ≈ 千秒
raw = new Uint8Array([
    (E >> 40) & 255,   // JS 32-bit signed: 等价 (E >> 8) & 0xff
    (E >> 32) & 255,   // JS 32-bit signed: 等价 E & 0xff
    (E >> 24) & 255,
    (E >> 16) & 255,
    (E >> 8)  & 255,
    E & 255,
])
```

⚠️ **关键陷阱**：JavaScript 位运算符 `>>` 是 32-bit signed，`E >> 40` **不是** `E >> 40`，而是 `E >> (40 % 32) = E >> 8`。Go 实现时**不能直接把 `>>40` 翻译成 Go 的 `>>40`**，需要精确复刻 JS 语义。

### 2. tVg 混淆函数

```js
function tVg(e) {
    let t = 165;
    for (let n = 0; n < e.length; n++) {
        e[n] = ((e[n] ^ t) + n) & 0xff;   // Uint8Array 自动 mod 256
        t = e[n];
    }
    return e;
}
```

Go 版：
```go
func obfuscate(raw [6]byte) [6]byte {
    var out [6]byte
    t := byte(165)
    for n := 0; n < 6; n++ {
        out[n] = ((raw[n] ^ t) + byte(n)) & 0xff
        t = out[n]
    }
    return out
}
```

### 3. base64 编码

标准 base64（**不是** url-safe），无 padding。6 字节 → 8 字符。

### 4. machineId / macMachineId

- **machineId**（64 字符十六进制）：Cursor **releaseHash**——SHA-256，每次 Cursor 版本升级都变
  - 3.10.20 的值 = `4071c661bcb367c518becc7b3d4d57cbd69d2291d8b302c558d79080f8fd4f75`
  - 从 `updateURL` 里能拿到：`api2.cursor.sh/updates/api/update/darwin/cursor/<version>/<releaseHash>/stable`
- **macMachineId**（64 字符十六进制）：**设备 UUID 派生的 SHA-256**
  - macOS：从 `ioreg -rd1 -c IOPlatformExpertDevice` 的 `IOPlatformUUID` 派生
  - 抓包实测：`df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab`

### 5. 缓存策略

**重要**：checksum **在 IDE 启动时算一次，长期复用**，不是每次请求都生成新的。所以时间戳字段实际上是"启动时刻的 6 字节混淆",不是"当前请求时刻"。

Go 实现：在 executor 构造时算一次并存在字段里。

## 验证样本

抓包时间：2026-07-09 22:54 (启动) → 23:00 (第一次请求)

抓到的 checksum: `kqyuuJOv4071c661bcb367c518becc7b3d4d57cbd69d2291d8b302c558d79080f8fd4f75/df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab`

拆解：
- base64 段 = `kqyuuJOv` (8 字符) → 混淆字节 `92acaeb893af`
- 反解混淆 → 原始 6 字节 `3739001b3739`
- 解读为 E = `0x001b3739` = **1,783,609**
- Date.now() ≈ 1,783,609,000,000 ms = **2026-07-09 22:56:40**（启动时刻）
- machineId = `4071c661...` (releaseHash for 3.10.20)
- macMachineId = `df1a4f6b...` (设备 UUID SHA-256)

**这三个成员完全对上抓包实测**。

## Go 参考实现

```go
package cursor

import (
    "encoding/base64"
    "encoding/hex"
    "fmt"
    "time"
)

// GenerateChecksum returns the value for x-cursor-checksum header.
// timestamp: caller decides when to snapshot (typically at session start).
func GenerateChecksum(timestamp time.Time, machineID, macMachineID string) string {
    E := timestamp.UnixMilli() / 1_000_000

    // JS 32-bit signed shift semantics
    raw := [6]byte{
        byte((int32(E) >> 8) & 0xff),    // JS: E >> 40 == E >> 8 (mod 32)
        byte(int32(E) & 0xff),           // JS: E >> 32 == E >> 0
        byte((int32(E) >> 24) & 0xff),
        byte((int32(E) >> 16) & 0xff),
        byte((int32(E) >> 8) & 0xff),
        byte(int32(E) & 0xff),
    }

    // Obfuscate (tVg)
    var obf [6]byte
    t := byte(165)
    for n := 0; n < 6; n++ {
        obf[n] = ((raw[n] ^ t) + byte(n)) & 0xff
        t = obf[n]
    }

    b64 := base64.StdEncoding.EncodeToString(obf[:])
    // Strip padding if any
    for len(b64) > 0 && b64[len(b64)-1] == '=' {
        b64 = b64[:len(b64)-1]
    }

    if macMachineID == "" {
        return fmt.Sprintf("%s%s", b64, machineID)
    }
    return fmt.Sprintf("%s%s/%s", b64, machineID, macMachineID)
}
```
