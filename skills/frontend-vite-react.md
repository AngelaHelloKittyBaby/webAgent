---
name: frontend-vite-react
description: 用 Vite + React 搭建或维护前端工程
---

# 何时使用
创建新的前端项目、或需要在 React 前端里加页面/组件/路由时。

# 步骤
1. 确认已有结构：没有前端工程就先初始化 `npm create vite@latest frontend -- --template react-ts`，然后 `npm install`
2. 组件放 `src/components/`，页面（路由级别的）放 `src/pages/`，每组件一个目录（`组件.tsx` + 可选 `组件.css`）
3. 路由用 `react-router-dom`：`<BrowserRouter>` + `<Routes>`，懒加载页面 `lazy(() => import(...))`
4. 状态管理：小项目用 `useState`/`useReducer` + Context 就够；确实需要跨模块共享再引 zustand
5. 改完跑 `npm run build` 验证，别让 TS 错误堵住构建

# 注意
- 组件 props 用 TypeScript 接口定义，别用 `any`
- 列表渲染给稳定 key，避免用数组下标当 key
- 表单赋值用受控组件或原生 form，别裸操作 DOM