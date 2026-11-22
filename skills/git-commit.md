---
name: git-commit
description: 提交 git 变更
---

# 何时使用
用户要求提交代码、commit 时。

# 步骤
1. 用 exec 运行 `git status` 看改动
2. 用 exec 运行 `git diff` 看内容
3. 根据改动写一条规范的 commit message
4. 用 exec 运行 `git add -A && git commit -m "..."`

# 注意
- commit message 用中文、简短、动词开头
