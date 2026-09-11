module github.com/ark-local-ai/ark/apps/agent

// go 1.21：最后一个官方支持 Windows 7 的 Go 线（1.22+ 已把 Win7 移出支持）。
// 本机 Go 1.25 也能编出 1.21 兼容产物：设 GOTOOLCHAIN=go1.21.13（自动拉取该工具链）。
// 依赖版本按 1.21 语言基线选（excelize v2.9.x / fsnotify v1.8.x）。
go 1.21

require (
	github.com/fsnotify/fsnotify v1.8.0
	github.com/xuri/excelize/v2 v2.9.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/richardlehane/mscfb v1.0.4 // indirect
	github.com/richardlehane/msoleps v1.0.4 // indirect
	github.com/xuri/efp v0.0.0-20240408161823-9ad904a10d6d // indirect
	github.com/xuri/nfp v0.0.0-20240318013403-ab9948c2c4a7 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/net v0.30.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/text v0.19.0 // indirect
)
