# Spark 接入 Cursor 本地 Agent：调研与待验证方案

## 结论与范围

用户已确认：第一版只覆盖 **Cursor 桌面本地 Agent**，点击必须定位**对应会话**；接受 **官方 Hooks + 只读本地会话元数据**，并优先验证**内置私有跳转链接**。精确选中后进入 **Agents 窗口可接受**；状态优先覆盖**运行、结束、未读**，精确 Waiting 及原因不作为第一版前提。本文是调研结果与建议，尚未实现产品代码、安装 hooks 或实际执行跳转。

监听有官方接口。精确跳转在本机 Cursor 3.24.12 随包代码中存在可用机制的静态证据，但有条件：`cursor://anysphere.cursor-deeplink/agent?id=<conversationId>` 转入 Agents / Glass 窗口，目标必须已在其 agent repository 中存在 header。本次没有证明任意经典 IDE 历史会话都能通过它打开，也没有做端到端验证。

建议先以“两条同项目会话能精确区分”为最小验证目标；通过后再实现完整采集。若私有链接无法覆盖经典 IDE，会需要重新决定是否接受扩展桥接，不能把仅打开项目当作精确跳转成功。

## 证据等级

- **官方文档**：Hooks 生命周期、字段、安装方式和公开 Deeplink 契约。
- **本机只读观察**：Cursor 版本、SQLite schema 和元数据字段名；不把字段名存在等同于运行状态语义已验证。
- **随包静态调用链**：URI dispatcher、Glass header 查找和会话选择、经典 IDE 内部命令。没有运行验证。
- **待验证推断**：hooks ID 与本地 header ID 的实际关联、私有链接对不同会话的覆盖、待处理标记的精确含义。

## Spark 现有接入位置

| 模块 | 现有职责 | Cursor 接入建议 |
| --- | --- | --- |
| `internal/monitor/state.go` | `SessionSource`、四态、`SessionItem`、`Jumpable()` | 增加 Cursor 来源；明确跳转能力，不让 default 分支误当 Claude |
| `internal/monitor/scanner.go` | 来源扫描、项目聚合、watch 目录 | 接入 Cursor 元数据与 hook 收据；缓存增量读取 |
| `internal/monitor/codexinstall.go` | 合并安装用户级 hooks | 借鉴幂等合并、保留用户配置的方式；Cursor JSON schema 需独立实现 |
| `internal/monitor/hooks.go` | hook 状态文件 | 沿用本地收据思路，使用独立 Cursor 命名空间 |
| `internal/monitor/deeplink.go` | Claude/Codex URL 构造与 ID 校验 | 增加 Cursor URL 构造与能力限制 |
| `view.go:1252` | `jumpToSession` 来源分发 | 显式处理 Cursor；异步执行，记录无法定位的失败 |

现有状态为 Running、Waiting、Unread、Completed。不能机械地把所有 Cursor 事件压成精确四态：未知、崩溃后陈旧状态和取消/错误都需要明确策略。

## 监听：Hooks 主导事件，元数据补全列表

### 官方 Hooks

来源：[Cursor Hooks](https://cursor.com/docs/agent/hooks)。

用户级配置为 `~/.cursor/hooks.json`，`version: 1`，`hooks` 把事件映射到命令数组。可覆盖本机所有项目；安装时需要合并并保留已有配置。Cursor 会监视配置变化；实际热重载行为仍应在目标版本验证。

关键关联字段：`conversation_id`（跨轮稳定）、`generation_id`（每次用户消息变化）、`hook_event_name`、`cursor_version`、`workspace_roots`。`sessionStart.session_id` 官方定义为与 `conversation_id` 相同。工作区是数组，不能静默只取第一项而丢失多根工作区关系。

| 事件 | 可观测语义 | 使用边界 |
| --- | --- | --- |
| `sessionStart` | 创建 conversation | 不代表每次恢复旧聊天都会触发 |
| `beforeSubmitPrompt` | 准备提交新一轮 | 其他 hook 可能阻止提交，不能独立证明已开始运行 |
| `preToolUse` / `postToolUse` / `postToolUseFailure` | 工具活动 | 单个工具失败不等于整轮失败；没有工具也可能在运行 |
| `afterAgentThought` / `afterAgentResponse` | 局部输出完成 | 不能代替整个 agent loop 结束 |
| `stop` | 本轮结束；`completed` / `aborted` / `error` | 不等于会话关闭；其他 hook 可能要求继续 |
| `sessionEnd` | 会话结束及原因 | 崩溃/强制退出是否必达没有保证 |
| `workspaceOpen` | 工作区打开/变化 | 没有 conversation ID，不能据此判断当前聚焦聊天 |

官方没有给出完整的焦点变化、已读变化、等待审批/问题的精确订阅契约，也没有承诺事件回放、严格有序、恰好一次。会话列表应通过元数据补全；不能用进程存活或固定超时独立判断某条会话仍在运行。

建议脚本只落盘白名单字段，以会话 ID + generation ID 关联。本地原子写入收据，避免 UI 扫到半个 JSON；事件缺 ID 时不归到“最近会话”。优先选择事件最少、足以表达轮次的组合，避免为状态监听保存完整 hook stdin。stdin 本身可能包含 prompt、工具输入、正文和邮箱，转发前应筛选。

### 本地元数据

本机只读核实的版本为 **3.24.12**，commit `37b865941ccb31e2fc90c685157534efca87f960`。

数据库路径：

```text
~/Library/Application Support/Cursor/User/globalStorage/state.vscdb
~/Library/Application Support/Cursor/User/workspaceStorage/<workspace-id>/state.vscdb
```

全局库的表为 `ItemTable`、`cursorDiskKV`、`composerHeaders`。此次使用 SQLite `mode=ro`，只输出 schema、键名、字段类型和计数；没有输出聊天正文、标题实际值或密钥。

`composerHeaders` 的列：

```text
composerId TEXT PRIMARY KEY
workspaceId TEXT
createdAt INTEGER
lastUpdatedAt INTEGER
isArchived INTEGER
isSubagent INTEGER
recency INTEGER
checkpointAt INTEGER
subagentTypeName TEXT
value TEXT
```

本机 21 条 header 的 `value` JSON 中观察到这些可选字段：`name`、`hasUnreadMessages`、`hasBlockingPendingActions`、`hasPendingPlan`、`workspaceIdentifier`、`lastUpdatedAt`、`agentLocation` 等；`name` 仅 5 条存在，`hasBlockingPendingActions` 仅 8 条存在，不能要求每行完整。`hasBlockingPendingActions` 的名字提示可作为 Waiting 候选，但尚未证明它能区分权限审批和模型提问；`hasPendingPlan` 也不能直接等同 Waiting。

`cursorDiskKV` 存在 `composerData:<id>`；所抽样对象含 `status`、`generatingBubbleIds`、`hasUnreadMessages` 等字段，也包含大体量会话信息。建议第一版优先读 `composerHeaders`，避免批量解析完整 composer。状态值、持久化时机和实时可靠性需要单独验证。

workspace 库的 `ItemTable['composer.composerData']` 在多个工作区存在，顶层有 `allComposers`、`selectedComposerIds`、`lastFocusedComposerIds`；这是旧/经典 IDE 适配线索，不能假设全局 headers 已覆盖所有历史会话。焦点记录也不独立证明用户已读。

数据库适配建议：只读连接、短查询、有界 busy timeout；同时考虑 WAL 及目录变动，避免只监听主文件 mtime；先做 schema 探测与可选字段容错。锁竞争/格式变化时保留可用 hook 信息，不能改写 Cursor 数据库。

### 状态归并建议（待运行验证）

- **Running**：以当前 generation 的可信活动事件驱动；旧 generation 的迟到 stop 不得覆盖新轮状态。
- **Waiting**：只有核实了阻塞标记与实际 UI 的对应关系才承诺；不能默认细分 Permission / Question。
- **Unread / Completed**：优先参考持久化的 `hasUnreadMessages`，并结合轮次是否已结束；点击链接成功发出不等于已读。
- **陈旧或未知**：不能永远显示 Running，也不能静默当成 Completed；实现前确定 UI/状态模型的退化策略。
- **归档、草稿、子会话**：默认过滤策略需要在验证阶段明确，避免空草稿和内部 subagent 淹没列表。

## 精确跳转：存在私有机制，覆盖需要实测

### 官方公开契约

[Deeplinks](https://cursor.com/docs/reference/deeplinks) 公开了 prompt、command、rule 等入口，未列出按本地 conversation ID 打开历史会话的接口。`/prompt?text=...` 是预填新提示词；`/command` 创建自定义命令，不能执行任意 workbench 命令。

[Extension API](https://cursor.com/docs/extension-api) 未公开枚举、监听或按 ID 打开本地聊天的稳定接口。CLI `agent --resume <chatId>` 针对 CLI 历史，不能当作 GUI 跳转；本次范围也已排除 CLI/云端。

### Cursor 3.24.12 随包静态证据

系统 `Info.plist` 注册 `cursor` scheme，`product.json` 信任 `anysphere.cursor-deeplink`。

候选外部 URL：

```text
cursor://anysphere.cursor-deeplink/agent?id=<URL编码后的会话ID>
```

接收代码路径：

```text
/Applications/Cursor.app/Contents/Resources/app/
  extensions/cursor-deeplink/dist/main.js
  out/vs/workbench/workbench.glass.main.js
  out/vs/workbench/workbench.desktop.main.js
```

扩展 dispatcher 的实际分支：

```js
"/background-agent" === A.path || "/agent" === A.path
// 该分支传入：
commands.executeCommand("cursor.openOrFocusGlassWindow", {
  agentsWindowOpenSource: "deeplink",
  startupHandoff: false,
  deeplinkUrl: t.toString(true)
})
```

上面是为阅读整理的等价片段；原包变量压缩命名，不作为运行验证。

已追到的调用链：

```text
cursor scheme
→ anysphere.cursor-deeplink /agent?id=...
→ cursor.openOrFocusGlassWindow
→ glass.handleDeeplink
→ handleCursorDeeplink → openAgent(id)
→ glass.openCloudAgentById
→ getAgentHeader(id)
    有 header：继续选择，不限制为云端 ID
    无 header、非 bc-：返回失败
    无 header、bc-：尝试补载云 agent
→ selectAgentRequested
```

因此 `openCloudAgentById` 虽然带 cloud 命名，静态逻辑可以接收已收录的本地 ID。**它进入 Agents / Glass 窗口，不保证回到经典 IDE 的原聊天标签页。** 对缺失 header 的本地 UUID 不会自动补载；不能承诺任意工作区、任意历史聊天均成功。

另一个外部入口 `/background-agent?bcId=...` 共用 openAgent，但推荐本地会话使用 `/agent?id=...`。内部协议 `cursor.agent://local/<UUID>` 有应用内 opener，Info.plist 未注册该 scheme，不能当作可从 Spark 直接打开的系统协议。

备用方案：扩展桥接 `vscode.commands.executeCommand('composer.openComposer', id, {openInNewTab: true})`。静态链已追到经典 IDE 的 composer 服务与 `showAndFocus`；仍是私有命令，且需要正确工作区。用户当前选择优先验证内置链接，因此暂不设计或安装桥接扩展。

## 推荐的下一步与验收

先做受控验证，不触发新模型任务，不更改已有聊天内容：

1. 确认一条本地 Agent 的 hook `conversation_id` 与 header `composerId` 对应；现有记录无法给出关联时，使用专用测试会话。
2. 同一个项目至少两条本地会话 A/B，分别打开 `/agent?id=A` 与 `/agent?id=B`，由实际 UI 确认选中的就是对应会话。
3. 再测经典 IDE 会话、跨项目、冷启动、关闭后重开及无效 ID。记录进入 Agents 窗口是否符合需求。
4. 验证提交、工具执行、完成、取消、审批阻塞、计划确认、切换会话已读后的事件和 header 变化；仅保留结构化状态证据。
5. 验证通过后实现 Cursor monitor、hooks 合并安装和跳转分发；状态用临时 SQLite fixture 与合成 hook payload 测试，补乱序、缺字段、schema 不支持和数据库锁竞争场景。

最小成功标准：Spark 列出两条本地会话，运行/结束能更新，点击每一条精确打开各自会话。只有发出 URL、激活 Cursor 或打开项目不算通过。

范围决策已收敛：接受 Agents 窗口作为精确跳转目的地；第一版优先运行/结束/未读，精确 Waiting 与原因后续增强。下一阶段先按上述用例验证私有链接覆盖，再确定实现，不能将本报告的静态证据当作已验收功能。

## 原始来源

- [Cursor Hooks](https://cursor.com/docs/agent/hooks)：schema、事件、用户级配置及排障。
- [Cursor Deeplinks](https://cursor.com/docs/reference/deeplinks)：公开 URI 契约。
- [Cursor Extension API](https://cursor.com/docs/extension-api)：公开扩展 API 的边界。
- [CLI Parameters](https://cursor.com/docs/cli/reference/parameters)：CLI resume 语义，不能代替 GUI。
- `/Applications/Cursor.app/Contents/Info.plist`、`Resources/app/product.json`：版本和系统协议声明。
- 随包 `extensions/cursor-deeplink/dist/main.js`：真实 URI 分发。
- 随包 `out/vs/workbench/workbench.glass.main.js`：Glass handler、header 查询、选择请求。
- 随包 `out/vs/workbench/workbench.desktop.main.js` 与 `out/vs/workbench/api/node/extensionHostProcess.js`：经典 composer 命令和扩展转发链。
- 本机 Cursor 用户库：只读 schema、字段名和统计；私有数据结构具有版本依赖。

原始并行调研留在 `/tmp/spark-cursor-research-agent.md` 和 `/tmp/spark-cursor-local-navigation.md`，后者有逐段原包切片、文件哈希与复现脚本。本文保留了用于决策的主要证据、限制与验证步骤，临时报告不作为稳定公共 API 的证明。
