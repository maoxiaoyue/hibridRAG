package services

import "errors"

// 業務錯誤（對照桌面版的 typed error catalog E3001/E3002/E3005；
// 手機版不帶 hypgo，改用標準 errors——訊息維持一致方便對照除錯）。
var (
	ErrNoConnection   = errors.New("Connection not set up yet, please check the Settings tab")
	ErrChatRoundLimit = errors.New("Chat round limit exceeded")
	ErrUploadEmpty    = errors.New("Please add at least one file first")
)
