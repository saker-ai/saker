# 中文文档

本目录存放 Saker 的**中文文档翻译**。

## 约定

- 顶层 `docs/*.md`（例如 `docs/architecture.md`、`docs/configuration.md`）
  目前仍是规范的英文文档，保持现状。
- 新文档应同时编写英文（`docs/en/`）与中文（`docs/zh/`）两份，
  在同一次提交中落地。
- 同步检查脚本（`scripts/check-doc-sync.sh`）比对 `docs/en/`
  与 `docs/zh/` 的文件名差集；当两边都存在同名文件时，会比较修改
  时间，对滞后的中文翻译发出告警。
- 中文文档与英文文档应保持同等深度，不做删减；技术术语和代码标识符
  保留原文。

## 迁移计划

顶层 10 个 `.md` 文件（`api-reference`、`architecture`、`configuration`、
`deployment`、`development`、`observability`、`overview`、`security`、
`testing`、`third-party-notices`）将在对应中文翻译完成时，与翻译稿
**同一次提交**一起迁入 `docs/en/`，确保同步状态从未中断。

## 为何不立即整体迁移？

一次性迁移会让外部对 `docs/architecture.md` 等链接全部失效，
而读者并不会因此获得任何新内容（英文文档本身未变）。逐对翻译、
逐对迁移可以让旧链接持续可用，直到每篇翻译就绪。

## 风格指南

- 中文使用全角标点（`，。：；！？「」`），代码、命令、文件路径
  使用半角并加反引号。
- 行内代码、变量名、API 名等技术标识符保留英文原文。
- 标题层级与英文版严格一致；保留英文锚点（heading）便于跨语言引用。
- 翻译时优先准确传达技术含义，避免逐字直译；当英文使用习惯表达
  （如 "out of the box"）时，意译为符合中文表达的对应说法。
