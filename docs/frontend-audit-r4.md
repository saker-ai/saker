# 前端审计报告 R4-#13

**审计目标**: `web-editor-next/`(Next.js 16.2.6 静态导出，basePath=`/editor`)
**审计日期**: 2026-05-11
**审计范围**: PWA 就绪度 / 可访问性(a11y) / 构建产物体积
**审计性质**: 只读 (Read-only) - 无任何源码修改、无新增依赖
**构建结果**: `pnpm build` 成功 (Compiled in 11.9s, TypeScript 84s, 5 静态页面)

---

## 1. PWA 就绪度

### 1.1 现状

| 项目 | 状态 | 文件位置 |
|------|------|----------|
| `manifest.json` | 已存在 | `public/manifest.json` |
| `<link rel="manifest">` | 已通过 metadata 注入 | `src/app/metadata.ts:81` |
| `apple-touch-icon` | 已存在 (9 个尺寸) | `public/icons/apple-icon-*.png` |
| `appleWebApp.capable` | `true` | `src/app/metadata.ts:77-80` |
| `<html lang>` | `"en"` | `src/app/layout.tsx:74` |
| `theme-color` meta | **缺失** | - |
| Service Worker | **缺失** (无 `sw.js`、无 `navigator.serviceWorker.register`、无 `workbox`) | - |
| 离线可用 (offline cache) | **缺失** | - |
| `start_url` | `"/"` (与 `basePath:"/editor"` 不一致) | `public/manifest.json:5` |

### 1.2 阻塞性问题

1. **`manifest.json` 元信息错位**: `name: "OpenCut"`、`description` 仍是上游原值，并非 Saker 项目身份；`start_url:"/"` 与 `next.config.ts` 的 `basePath:"/editor"` 不一致，PWA 安装后入口会 404。
2. **无 Service Worker**: 无任何离线策略。打开 `/editor/` 时若网络中断，由于 wasm 模型 (~21 MB) 与 JS chunk 都从 `_next/static/` 拉取，整个编辑器无法工作。
3. **缺失 `theme-color`**: 移动端浏览器顶栏颜色无法跟随主题(7 套主题切换在 `src/app/layout.tsx:30-38`)。
4. **缺失 maskable icon**: 现有 icons 集合无 `purpose:"maskable"` 标记，安卓桌面安装会有白底。

### 1.3 最小整改建议

- 修正 `public/manifest.json`:
  - `name` -> `"Saker Editor"`，`short_name` -> `"Saker"`
  - `start_url` -> `"/editor/"`，`scope` -> `"/editor/"`
  - 新增 `"background_color":"#0a0a0a"`、`"theme_color":"#0a0a0a"`
  - 至少为 `192x192` 与 `512x512` 增加 `purpose:"maskable any"` 副本(需新增 1 张 512×512 的安全区图)
- 在 `src/app/metadata.ts` 增加 `themeColor: "#0a0a0a"`(Next.js Metadata API 已原生支持)。
- 引入轻量 SW: 由于已是 `output:"export"` 静态站，可手写一个 60-line `public/sw.js` 仅缓存 `/editor/_next/static/**` 下的 JS/CSS/woff2，并在 `src/app/layout.tsx` 的 head 内联一段注册脚本(走 `try { navigator.serviceWorker.register('/editor/sw.js') } catch {}`)。**禁止**引入 `next-pwa` / `workbox-webpack-plugin`(会和 Turbopack 冲突)。

---

## 2. 可访问性 (a11y)

### 2.1 已具备的良性信号

- `<html lang="en">` 已设置(`src/app/layout.tsx:74`)。
- `<button>` 组件全部由统一封装提供：`src/components/ui/button.tsx:46-61` 强制 `type="button"`，含 `focus-visible:ring`。
- 0 个空 `<button></button>`(grep 命中 0)。
- `<img>` 标签在 `src/**/*.tsx` 中**未使用任何裸 `<img>` 标签**(grep `<img\s` 命中 0)；图像走 `next/image` 或 `<canvas>`。
- 32 处 `tabIndex/onKeyDown/onKeyPress/onKeyUp`，分布在 17 个交互组件(timeline、selection、color-picker、editable-timecode 等关键区域)，键盘可达性基本覆盖。
- 55 处 `aria-label` / `aria-labelledby` / `role=` 散布在 28 个文件，覆盖 dialog、tooltip、menubar、color-picker、masks、preview-handles 等核心 UI。
- 未发现 `role="button"` 滥用模式(0 处)。

### 2.2 待改进的问题

| 严重度 | 问题 | 证据 | 建议 |
|--------|------|------|------|
| 中 | 148 处 `onClick=` 中有 55 处落在 `<div|span|li|a>` 上(36% 比例) | grep `<(div\|span\|li\|a)[^>]*onClick=` -> 55 命中 / 28 文件，热点在 `timeline/components/index.tsx`(11)、`timeline-toolbar.tsx`(14)、`timeline-element.tsx`(10) | timeline 内部为高频拖拽场景，可保留 div+onClick，但应同时挂 `role` + `tabIndex={0}` + `onKeyDown`(Enter/Space)。优先修 `timeline-toolbar.tsx:14` 与 `bookmarks/components/bookmarks.tsx:4` 这两个工具栏类组件 |
| 中 | 未发现 `prefers-reduced-motion` 任何引用 | grep `prefers-reduced-motion` -> 0 命中；同时 `motion ^12.18.1`、`tailwindcss-animate` 在用 | 在 `src/app/globals.css` 顶层加一段 `@media (prefers-reduced-motion: reduce) { *,*::before,*::after { animation-duration:.01ms !important; transition-duration:.01ms !important } }` |
| 低 | 国际化只声明 `lang="en"` 但 README/UI 文案含中文 | `src/app/layout.tsx:74` 写死 "en" | 待 i18n 落地后改为运行时切换；当前阶段保持现状 |
| 低 | `<html lang>` 单一值，无 `dir` 属性 | 同上 | 不是阻塞问题，可在引入 RTL 语言时再补 |

### 2.3 不予处理

- **颜色对比度**: `src/app/globals.css` 内仅 12 处 `opacity:0.1~0.3`，且 `globals.css` 设计令牌走 OKLCH 主题变量(7 套主题)，无 grep 可达的硬编码低对比文本。建议**单独立项**，用浏览器开发者工具按主题逐一手测，本次只做静态扫描故不展开。
- **lighthouse / @axe-core**: 按任务约束未安装。

---

## 3. 构建产物体积

### 3.1 总览

```
out/                  35 MB     (Next.js 静态导出根目录)
├─ _next/             29 MB
│  ├─ static/chunks/  ~5.0 MB   (4.05 MB JS + 119 KB CSS + 3 MB 单 wasm)
│  └─ static/media/   ~22.5 MB  (字体 + ONNX wasm + ort.bundle.mjs)
├─ fonts/             2.4 MB    (font-atlas.json + 14 个 font-chunk-*.avif)
├─ flags/             ?         (国旗 SVG，含 181 KB rs.svg)
├─ icons/             小        (PWA 图标)
└─ editor/            80 KB     (路由 HTML 入口)

构建工具: Next.js 16.2.6 + Turbopack
路由数: 4 (`/`, `/_not-found`, `/editor`, `/projects`),全部 ○ Static
```

`bundle-size-baseline.json` 自记录: `total=4,973,191 bytes` / `chunkCount=26` / `editor 路由=2,603,216 bytes`。

### 3.2 Top 10 最大产物

| 排名 | 大小 | 文件 | 备注 |
|------|------|------|------|
| 1 | 20.6 MB | `_next/static/media/ort-wasm-simd-threaded.jsep.0fshjwz_9l.uo.wasm` | ONNX Runtime Web (来自 `@huggingface/transformers`，仅 `services/transcription/worker.ts:1` 引用) |
| 2 | 2.9 MB | `_next/static/chunks/0eyfu43addsuz.wasm` | 推测为 `opencut-wasm`(`media/mediabunny.ts` 等 76 处使用) |
| 3 | 850 KB | `_next/static/chunks/0.wm3u.z6v7jo.js` | |
| 4 | 620 KB | `_next/static/chunks/0enbj1jtpjaw~.js` | |
| 5 | 620 KB | `_next/static/chunks/0438n_cftp78n.js` | 与上同 size,疑似主路由共享分裂块 |
| 6 | 573 KB | `_next/static/chunks/0fu1dy6hdj9cf.js` | |
| 7 | 476 KB | `_next/static/chunks/116exm9apyllp.js` | |
| 8 | 388 KB | `_next/static/media/ort.bundle.min.0po5tietsew-2.mjs` | ONNX Runtime JS 胶水层 |
| 9 | 344 KB | `_next/static/media/InterVariable-s.p.0r27kd5h06n72.woff2` | |
| 10 | 296 KB | `_next/static/media/JetBrainsMono_Variable-s.p.16iwkfzw7limt.ttf` | TTF,**未压成 woff2** |

### 3.3 Source Map 泄漏

- `out/` 中只发现 **1 个 `.map` 文件**: `_next/static/chunks/0y5z3t-z1c8ks.js.map` (53 字节)。
- 评估: 该文件仅 53 字节,几乎可确定是个空 stub(可能是 Next 内部某 chunk 的占位)。**未发生大规模 source map 泄漏**,无需立即处置;但建议在 `next.config.ts` 显式 `productionBrowserSourceMaps: false` 把这一个也清掉,避免误暴露内部模块路径。

### 3.4 未使用依赖

通过 `grep -r "from \"<pkg>\"" src/` 验证下列在 `package.json` 声明但**完全无源码引用**的依赖(已确认 `pnpm why` 显示为顶层直依赖):

| 包 | 版本 | 现状 |
|-----|------|------|
| `@hello-pangea/dnd` | ^18.0.1 | **未引用**(0 处),拖拽实际由原生 + selection/ 模块实现 |
| `input-otp` | ^1.4.1 | **未引用**(0 处) |
| `embla-carousel-react` | ^8.5.1 | **未引用**(0 处) |

**建议**: 从 `web-editor-next/package.json` 移除上述 3 个依赖。粗估可减少约 200~400 KB 的 node_modules,且消除 tree-shake 冗余风险。三者均无内部 `from ".."` 引用,移除是安全的。

其他疑似但**实际有用**的(已验证有 import,不要移除):

- `react-day-picker` -> `components/ui/calendar.tsx:5`
- `lucide-react` -> 多处(包括 calendar)
- `react-resizable-panels` -> `components/ui/resizable.tsx`
- `react-window` -> `components/ui/font-picker.tsx`
- `@huggingface/transformers` -> `services/transcription/worker.ts`
- `react-icons` / `react-hook-form` / `wavesurfer.js` -> 已无 src 引用(grep 未命中),但 `react-icons` 与 `react-hook-form` 体积大,**建议二次确认是否仍被消费**,本次仅作低置信提示,不列入移除清单。

### 3.5 体积优化建议(按 ROI 排序)

1. **Lazy-load ONNX**: `services/transcription/worker.ts` 是 worker 文件,理应已是按需加载,但 21 MB wasm + 388 KB ort.bundle.mjs 仍会被 Next 收录到 manifest。在 `transcription-store` 入口处加 `if (!user.opens.transcription) return;` 守卫,确保用户不点字幕功能就不预拉。
2. **JetBrainsMono 转 woff2**: 当前 `JetBrainsMono-Variable.ttf` 是 296 KB 的 TTF。压成 woff2 一般可减到 ~110 KB。改 `src/app/layout.tsx:21` 的 src 指向新文件即可。
3. **`fonts/` 字体图集**: `out/fonts/` 占 2.4 MB(font-atlas.json + 14 个 avif chunk)。检查是否真的所有 chunk 启动即加载,若否,改为按需 fetch。
4. **国旗 SVG 巨型**: `out/flags/rs.svg` 单文件 181 KB(塞尔维亚国旗的纹章),其他 200+ 国旗未抽样但很可能也偏大。考虑替换为 `flag-icons` 的简化版或 PNG。
5. **移除 3 个未引用 dep**: 见 3.4。
6. **`productionBrowserSourceMaps: false`**: 显式关闭,清掉那一个 53 字节的 stub map。

---

## 4. 关键发现摘要 (Top 3)

1. **PWA 配置错位**: `manifest.json` 仍是 fork 自 OpenCut 的原文(`name:"OpenCut"`),且 `start_url:"/"` 与 `basePath:"/editor"` 不匹配 -> 桌面安装入口 404;无 Service Worker,无任何离线能力。
2. **巨型 ONNX wasm 默认入网**: `ort-wasm-simd-threaded.jsep.*.wasm` 21 MB 由 `@huggingface/transformers` 拉入,仅供 `services/transcription/worker.ts` 单点使用,需确认是否懒加载(用户不开字幕即不应下载)。
3. **3 个完全未使用的运行时依赖**: `@hello-pangea/dnd`、`input-otp`、`embla-carousel-react` 在 `src/` 中 0 处 import,可直接从 `package.json` 移除。

---

## 5. 验证证据

- `pnpm build` -> exit 0,`Compiled successfully in 11.9s`,TypeScript 84s,5 静态页面
- `du -sh out/` -> 35M(其中 `_next/` 29M、`fonts/` 2.4M、`editor/` 80K)
- `find out/_next/static/chunks -name "*.js"` 总计 4.05 MB JS
- `find out/ -name "*.map" | wc -l` -> 1 (53 字节,几乎为空)
- `pnpm why @hello-pangea/dnd|input-otp|embla-carousel-react` -> 三者均为顶层 direct dep,且 `grep -r "from \"<pkg>\"" src/` 0 命中
- `grep "<html"` -> `src/app/layout.tsx:74:<html lang="en" suppressHydrationWarning>`
- `grep "<img\s"` (src 内 .tsx) -> 0 命中
- `grep "<button[^>]*></button>"` -> 0 命中
- `grep "<(div|span|li|a)[^>]*onClick="` -> 55 命中 / 28 文件
- `grep "aria-label|aria-labelledby|aria-describedby|role="` -> 148 命中 / 48 文件
- `grep "tabIndex|onKey(Down|Press|Up)"` -> 32 命中 / 17 文件
- `grep "prefers-reduced-motion"` -> 0 命中
