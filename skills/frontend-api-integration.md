---
name: frontend-api-integration
description: 前端对接后端 API（fetch 封装、错误处理、加载态）
---

# 何时使用
前端要调后端接口、处理请求错误和加载状态时。

# 步骤
1. 封装一个 `api` 模块（基于 fetch），统一：baseURL、`Content-Type: application/json`、token header
2. 请求生命周期进状态机：`loading` / `error` / `data` 三个状态，用 `useState` 或 `useReducer` 管理
3. 错误处理：非 2xx 时读响应体里的 `message` 字段，显示给用户而不是打印裸状态码
4. AbortController 取消请求：组件卸载或请求过期时 `controller.abort()`，避免 setState on unmounted
5. 竞态保护：响应的请求 id 和最新的比，不一致就丢弃（防止慢请求覆盖新结果）

# 注意
- 别在主组件里堆 fetch，抽 `useFetch<T>(url, deps)` 之类自定义 hook
- token 放 `localStorage` 或内存，别放 url query
- 请求失败给"重试"按钮，别只显示错误