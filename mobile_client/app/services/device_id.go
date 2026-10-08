// device_id.go — 「一 Key 一裝置」的裝置指紋。
//
// 桌面版由 Windows MachineGuid 推導穩定 ID；Android 上純 Go 取不到等價的
// 穩定機器特徵（ANDROID_ID 需要 JNI），一律走「首次啟動產生隨機 ID 並持久化」
// 路徑——重灌 app（清掉私有目錄）視同新裝置，符合一 Key 一裝置語意，
// 換機時由部署機執行 `gateway apikey unbind` 重綁。
package services

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomDeviceID 產生一次性的隨機 device id（呼叫端負責存進 config）。
func RandomDeviceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "and_" + hex.EncodeToString(b)
}
