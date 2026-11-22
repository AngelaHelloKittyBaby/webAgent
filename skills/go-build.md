---
name: go-build
description: 编译和测试这个 Go 项目
---

# 何时使用
需要编译或测试这个 Go 项目时。

# 步骤
1. 用 exec 运行 `go build ./...`
2. 用 exec 运行 `go test ./...`
3. 有报错就 read_file 读报错的文件，修复后重跑

# 注意
- 先 build 通过再 test
