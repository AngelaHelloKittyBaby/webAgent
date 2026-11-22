---
name: backend-go-http-api
description: 用 Go 写 HTTP API（路由、JSON、错误处理、中间件）
---

# 何时使用
写 Go 后端接口、加路由/中间件、统一错误响应时。

# 步骤
1. 路由：小项目用 `http.ServeMux`（Go 1.22+ 支持方法+路径模式如 `"POST /api/users"`）；复杂路由再上 chi
2. Handler 只做三件事：解析请求 → 调业务函数 → 写响应。业务逻辑放 service 层，别堆在 handler 里
3. 统一 JSON 响应结构：`{"code":0,"data":...,"message":"ok"}` 或简单的 `{"error":"..."}`——选一种并全程一致
4. 错误处理：handler 里 `if err != nil { writeError(w, http.StatusBadRequest, err); return }`，错误消息对客户端只暴露安全部分，细节进日志
5. 中间件链：logging、recovery（panic→500）、CORS、鉴权，用 `func(next http.Handler) http.Handler` 组合
6. 上下文穿透：`r.Context()` 传 request-scoped 值（用户 id、trace id），别用全局变量

# 注意
- JSON 序列化用 `json.NewEncoder(w).Encode(v)`，状态码先 `w.Header().Set` 再 `w.WriteHeader`
- 敏感字段（密码）加 `json:"-"` 不导出
- 中间件顺序：recovery 最外层，logging 其次，最后才是业务中间件