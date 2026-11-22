---
name: frontend-tailwind-styling
description: 用 Tailwind CSS 写样式（工具类、响应式、暗色模式）
---

# 何时使用
前端页面要加样式、要做响应式或暗色模式时。

# 步骤
1. 确认 Tailwind 装了：`tailwind.config.js` 的 content 要包含 `./src/**/*.{ts,tsx}`，CSS 里 `@tailwind base; components; utilities`
2. 布局用 `flex` / `grid` 工具类：`flex items-center justify-between`、`grid grid-cols-3 gap-4`
3. 间距/字号/颜色优先用默认刻度：`p-4`、`text-sm`、`text-slate-600`，别自定义任意值（除非必须 `text-[13px]`）
4. 响应式断点前缀：`sm:`(640) `md:`(768) `lg:`(1024) `xl:`(1280)，移动优先——先写基础样式再写断点覆盖
5. 暗色模式：html 上加 `class="dark"`（tailwind.config `darkMode: 'class'`），深色用 `dark:bg-slate-900` 覆盖

# 注意
- 样式写在 JSX 的 className 里，禁止内联 `style={{}}` 做布局
- 提取重复样式成小组件或 `@apply`，别在十几个地方复制同一串类名
- 语义色（成功/警告/错误）统一用一套 token，别随意混色