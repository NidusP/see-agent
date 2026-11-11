# 样例资产生成说明

## 目的

记录 V1 摄取样例文档的生成方式、依赖路径和验证步骤，方便后续会话重复生成或扩展样例。

## 当前样例覆盖

- `txt`: `data/sample-docs/employee-handbook.txt`
- `md`: `data/sample-docs/employee-handbook.md`
- `json`: `data/sample-docs/policy-catalog.json`
- `pdf`: `data/sample-docs/sample-policy.pdf`
- `docx`: `data/sample-docs/sample-handbook.docx`
- `xlsx`: `data/sample-docs/sample-budget.xlsx`
- `pptx`: `data/sample-docs/sample-training-deck.pptx`

## 生成策略

- `txt` / `md` / `json`
  - 直接作为文本与结构化样例维护在仓库中
- `pdf`
  - 使用 bundled Python + `reportlab` 生成
- `docx`
  - 使用 bundled Python + `python-docx` 生成，并用 `render_docx.py` 渲染检查
- `xlsx`
  - 使用 bundled Node + `@oai/artifact-tool` 生成并渲染关键 sheet
- `pptx`
  - 使用 bundled Node + `@oai/artifact-tool` 生成并渲染 slide PNG 做视觉检查

## 运行时依赖

- Node:
  - `/Users/populus/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node`
- Node modules:
  - `/Users/populus/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules`
- Python:
  - `/Users/populus/.cache/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3`

## 生成入口

- `scripts/dev/build_sample_documents.py`
- `scripts/dev/build_sample_workbooks.mjs`

## 推荐命令

- 生成 `txt` / `md` / `json` / `pdf` / `docx`
  - `/Users/populus/.cache/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3 scripts/dev/build_sample_documents.py`
- 生成 `xlsx` / `pptx`
  - `/Users/populus/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node scripts/dev/build_sample_workbooks.mjs`

## 当前工作区约束

- 根目录 `node_modules` 需要能解析到 bundled runtime 的 `node_modules`
  - 当前工作区已通过符号链接满足该条件，便于 `@oai/artifact-tool` 直接运行
- 本机未提供 `LibreOffice` / `soffice`
  - `docx` 无法走完整 LibreOffice 渲染校验链
- 本机未提供 `pdftoppm`
  - `pdf` 无法走 Poppler PNG 渲染链
- 当前文档视觉校验的实际兜底方案：
  - `docx` / `pdf` 使用 macOS `qlmanage` 生成预览图
  - `xlsx` / `pptx` 使用 `@oai/artifact-tool` 直接渲染 PNG

## 验证要点

- `docx` 至少检查第一页 PNG
- `pdf` 至少检查第一页 PNG
- `xlsx` 检查 summary / assumptions sheet 渲染
- `pptx` 检查 slide PNG 和整体版式

## 当前验证结果

- `docx`
  - 已通过新的临时副本 `qlmanage` 预览复核，标题、列表和引用表结构可读
- `pdf`
  - 已通过 Quick Look 预览复核，单页版式正常
- `xlsx`
  - `sample-budget-preview.png` 已复核，可读性正常，无明显列裁切
- `pptx`
  - `sample-training-deck-slide-01.png`、`slide-02.png`、`slide-03.png` 已复核
  - 第 2、3 页表格溢出问题已通过缩短文案和调整表格宽度修复
