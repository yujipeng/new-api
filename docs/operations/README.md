# NewAPI 运营操作手册

> 适用版本：仓库基线 `2d1ca153`（2026-05-21）
> 编写源：项目内代码事实层调研（每条结论附「文件:行号」证据）+ 运营场景翻译
> 适用对象：第一次接手 NewAPI 运营 / 客户成功的同学
>
> **目标**：照着这份文档，从零完成平台配置、客户接入，并完整理解计费与日志统计。

---

## 章节速查

| # | 文件 | 内容 |
|---|---|---|
| 1 | [`01-platform-setup.md`](01-platform-setup.md) | 平台搭建上线（供应商 → 模型 → 价格 → 分组 → 渠道） |
| 2 | [`02-customer-onboarding.md`](02-customer-onboarding.md) | 客户接入（用户 → 令牌 → 充值 / 兑换码） |
| 3 | [`03-billing.md`](03-billing.md) | 计费规则（标准 / 分级 / 音频 / 任务，含人话版与原始公式 + 量纲） |
| 4 | [`04-logs-stats.md`](04-logs-stats.md) | 日志与数据看板（`logs` / `quota_data` 表 + 前端入口 + 统计口径） |
| 5 | [`05-faq.md`](05-faq.md) | 常见运营场景速查 / 排错（13 条灰区翻译为运营行动建议） |
| 99 | [`99-pending-items.md`](99-pending-items.md) | 灰区证据闭环追踪表（13 条全部闭环）+ 已知改进点附录（PJM 另开 issue 跟踪） |

---

## 阅读建议

- **第一次接手** → 顺序读 1 → 2 → 3 → 4 → 5。
- **只想配置渠道 / 模型 / 分组** → 读第 1 章。
- **客户对账 / 排错** → 直接跳到第 5 章，按客户反馈对应「场景 X」。
- **计费金额疑问** → 第 3 章先看 3.0「量纲」+ 3.1.1「人话版公式」。
- **后台数据库查询** → 第 4 章 4.7「表与统计速查」+ 第 5 章 5.4「常用查询语句」。

---

## 关键约束（运营层必须记住）

1. **量纲：1 USD = 500000 quota**（详见 [`03-billing.md` 3.0](03-billing.md#30-量纲先记住这三条)）。
2. **三层 group 不是同一个东西**：`users.group` / `tokens.group` / `channels.group`，详见 [`01-platform-setup.md` 1.3](01-platform-setup.md#13-步骤三定义分组重点章节)。
3. **`GroupRatio` 与 `UserUsableGroups` 必须双写**，否则鉴权 403。
4. **新模型上线必须显式注册 `ModelRatio`**，否则扣费行为不确定（追踪表 #1）。
5. **`logs.ip` 默认不记**；按 IP 排错前需先开启（追踪表 #7）。
6. **`rpm` / `tpm` 是「最近 60 秒」截面值**，不是区间平均（追踪表 #11）。

---

## 文档维护规约

- 本手册落在仓库 `docs/operations/` 目录。
- 文档变更走 `feature/<topic>` 分支，**禁止直接 push 到 main / develop**（v5 规约）。
- 三方一致性反馈（PRD ↔ 文档 ↔ 代码）通过父 issue 评论上报对应责任人。
- 灰区结论与已知改进点统一进 [`99-pending-items.md`](99-pending-items.md)；正文相关位置仅做「⚠️ 详见末尾追踪表 #N」标注。

---

## 证据基线

- 仓库：`https://github.com/yujipeng/new-api`
- 基线：`origin/main` HEAD = `2d1ca153 fix: respect dashboard content visibility settings (#4975)`
- 摸底来源：父 issue [TES-69](mention://issue/7628a2ad-bf09-4050-b197-98910ff11357) 中架构师 `e2af1c61` 评论「NewAPI 运营事实摸底报告」
- 编写分支：`feature/docs-operations-manual`
