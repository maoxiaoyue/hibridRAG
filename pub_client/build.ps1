# build.ps1 — 建置 pub_client（Windows 桌面 client，外網版）。
#
# 關鍵：-ldflags "-H=windowsgui" 把程式標成 GUI 子系統，執行時「不會」附帶
# 黑色 console 視窗。少了這個旗標，雙擊 exe 會多開一個 CLI 視窗。
#
# 需求：CGO（Fyne GL driver）+ gcc（MinGW-w64）。用法：於本目錄執行 .\build.ps1
$env:CGO_ENABLED = "1"
go build -ldflags "-H=windowsgui" -o pub_client.exe .
if ($LASTEXITCODE -eq 0) { Write-Host "Built pub_client.exe (no console window)" }
