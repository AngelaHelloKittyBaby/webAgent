---
name: fullstack-project-layout
description: 全栈项目目录结构与前后端联调规范
---

# 何时使用
搭全栈项目、或前后端联调出现对接问题时。

# 步骤
1. 推荐的顶层结构：`frontend/`（React+Vite）、`backend/`（Go）、`docs/`——各自独立 package/go module
2. 前后端约定接口清单：先定 URL、方法、请求/响应 JSON 结构，再写代码，避免两头猜
3. 开发期联调：前端 vite proxy 指向后端（`server.proxy: { '/api': 'http://localhost:8080' }`），避免 CORS 问题
4. 共享契约：手写或生成 API 类型（OpenAPI → typescript），至少把响应结构写进接口文档
5. 环境配置：端口、数据库地址走 `.env` / 环境变量，前后端各一份示例文件 `.env.example`

# 注意
- 错误响应前后端统一语义：`{"error":{"code":"NOT_FOUND","message":"..."}}`，前端按 code 分支
- 时间一律 UTC 传输 + 前端本地化展示
- 别让前端直接拼 SQL 或拿库连接——一切经后端 API