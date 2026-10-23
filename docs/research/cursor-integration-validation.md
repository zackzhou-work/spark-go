# Cursor 本地 Agent 接入：P0 验证记录

状态：P0 通过（含一项未控制项与若干未执行项，见下）。  
验证日期：2026-10-23。  
方案：[cursor-local-agent-integration.md](../plans/cursor-local-agent-integration.md)；调研：[cursor-local-agent-integration.md](cursor-local-agent-integration.md)。

## 1. 环境

| 项 | 值 |
| --- | --- |
| macOS | 27.0.1（build 26A434），Apple M4 Pro |
| Cursor | 3.24.12，commit `37b865941ccb31e2fc90c685157534efca87f960` |
| Cursor 安装 | 仅 `/Applications/Cursor.app` 一份；`Info.plist` 以 `Editor` 角色声明 `cursor` scheme |
| URL handler | 系统 `open` 直接落到该安装（三次 `open cursor://…` 均按预期选中，见 §4） |
| 数据目录 | `~/Library/Application Support/Cursor/User/{globalStorage,workspaceStorage}` |
| Spark 采集 | 本次从工作树构建的二进制安装到 `~/.config/spark/bin/spark-cursor-hook`，collector 版本 1 |

读取方式：仅只读 SQLite（`mode=ro`）与 Spark 自己的白名单事件文件。没有读取 transcript、`composerData:<id>` 正文、prompt、工具内容或邮箱；本记录不含真实会话标题以外的聊天内容，标题仅用于人工点击识别。

## 2. 逐项结果（方案 §4.1）

| # | 项目 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 版本 / commit / 系统 / handler | 通过（记录，见 §1） | `product.json` 的 `commit`；`package.json` 的 `version`；`open` 三次成功路由 |
| 2 | `hook.conversation_id == composerHeaders.composerId` | 通过 | 24 个事件涉及 5 个 conversation，全部在 `composerHeaders` 中命中（5/5），无需靠标题猜测 |
| 3 | 同项目两条本地会话 A/B 精确选中 | 通过 | A `5f32ab93…` → UI 显示 A；B `f121ab82…` → UI 显示 B；两条同属 `workspaceId ad1909260e41dba1c526ad0b14a755f7` |
| 4 | 跨项目 / 窗口开关 / 冷启动 / 已结束会话 | 部分通过 | 跨项目 `2458343b…`（`workspaceId 7770c3c9…`）→ UI 显示该会话；已结束会话（`completed` 后）同样可选中。**冷启动（退出 Cursor 后点击）与 Agents 窗口关闭两种情形未单独控制执行** |
| 5 | 经典 IDE 旧会话 | 未执行 | 见 §5：经典布局 28 个 composerId 与 `composerHeaders` 交集为 0，第一版因此不启用经典布局 |
| 6 | 无效 ID 不建新会话、不提交任务 | 通过 | `/agent?id=00000000-0000-0000-0000-000000000000` → Cursor 自身报错，`composerHeaders` 行数不变（29），无新 generation；三次有效点击同样未新增会话或轮次 |
| 7 | 一轮提交/生成/stop/取消的事件名与字段 | 部分通过 | 观察到 `sessionStart`、`beforeSubmitPrompt`、`afterAgentThought`、`afterAgentResponse`、`postToolUse`、`stop`（`status=completed`）×3。**未观察到 `sessionEnd`**（验证期间没有关闭会话）与 `postToolUseFailure`；取消（`aborted`）未执行 |
| 8 | headers 标题/工作区/未读/归档/草稿/本地-云端/子会话字段形状与枚举 | 通过 | 见 §3 |
| 附 | 未读字段与 UI 变化的对应 | 通过（数据库侧） | 未聚焦结束的轮次把 `hasUnreadMessages` 从 false 翻到 true；聚焦结束的轮次保持 false。**未用截图与 Cursor 侧边栏逐帧对照** |

## 3. `composerHeaders` 实际形状（3.24.12）

列（全部 10 列存在，类型与调研一致）：`composerId TEXT PK`、`workspaceId TEXT`、`createdAt INTEGER`、`lastUpdatedAt INTEGER`、`isArchived INTEGER`、`isSubagent INTEGER`、`recency INTEGER`、`checkpointAt INTEGER`、`subagentTypeName TEXT`、`value TEXT`。

`value` 中与列表相关的字段与观察到的取值：

| 字段 | 形状 / 枚举 | 观察 |
| --- | --- | --- |
| `type` | `"head"` | 29/29 |
| `unifiedMode` | `"agent"` \| `"chat"` | 两种都出现 |
| `name` | string | 部分会话没有；缺失时必须显示 `Untitled Session`，不能拿第一条 prompt 补 |
| `hasUnreadMessages` | bool | 聚焦结束 false；未聚焦结束 true |
| `isDraft` | bool | true 的行 `hasBeenInSidebar` 也为 true，属于未提交草稿 |
| `isArchived` / `isSpec` / `isProject` / `isBestOfNSubcomposer` | bool | 均观察到 true 行，需排除 |
| `hasBlockingPendingActions` / `hasPendingPlan` | bool | 存在但第一版不使用：没有验证它们与「等待审批 / 等待提问」的对应关系 |
| `workspaceIdentifier` | `{id, uri{scheme:"file", path, fsPath, external, $mid}}` | `path` 即绝对项目路径，可直接分组；部分旧行只有 `{id}` |
| `agentLocation` | `{type, status, environment{id}}` | 观察到 `type="local"`, `status="active"`；`cloud` / `background` 仅作排除用 |
| `conversationCheckpointLastUpdatedAt` | int(ms) | 与 `checkpointAt` 列同值 |

`workspaceStorage/<id>/workspace.json` 为 `{"folder":"file:///…"}`，用于补 `workspaceIdentifier.uri` 缺失的旧行。

## 4. 时间语义（关键发现，修正了方案 §8.2 的假设）

以一次计时精确的轮次为例（毫秒，相对首个事件）：

```text
+    0 sessionStart
+  119 beforeSubmitPrompt          ← header.lastUpdatedAt / recency 在此附近写入
+ 4009 afterAgentThought
+ 4236 afterAgentResponse
+ 4518 stop completed              ← 终态
header.lastUpdatedAt  -168
header.checkpointAt  +3991        ← 最后一次内容写入，比 stop 早 322~527ms
```

结论：

- **`lastUpdatedAt` / `recency` 在轮次开始时写入，不在结束时更新。** 「header.UpdatedAt ≥ 终态时间」在这版 Cursor 上不可能成立，按方案原文实现会让所有结束的轮次永远停在 Unknown。
- `checkpointAt` 表示最后一次内容落地，仍早于 `stop`（本次 322ms 与 527ms）。
- 因此可用且已验证的关联条件是**读取时刻**：元数据在终态事件收到之后（且留 2 秒落定）再读一次，读到的 `hasUnreadMessages` 就是该轮结束后的状态。Spark 的 reducer 用这个条件（`cursorMetadataSettle`），没用 header 时间戳。

另有两个会导致误判的行为，已在实现中处理：

- **`sessionStart` 会在恢复/打开会话时触发**：hooks.json 生效后一次批量出现 5 个 `sessionStart`，其中 4 个是已有会话被重新打开（`workspace_roots` 为空）。它既不能当作轮次，也不能当作活动时间，否则旧会话会被拉到「Today」。
- **只有 `sessionStart` 的会话没有 header**：观察到一个 conversation 在 `composerHeaders` 中出现后又消失。第一版只展示有 header 的会话，这类事件不会变成列表项。

## 5. 会话范围

| 类别 | 判断依据 | 第一版处理 |
| --- | --- | --- |
| 现代 Agents 本地会话 | `composerHeaders` 行 + `agentLocation.type="local"` 或无该字段 | 展示，状态按证据 |
| 云端 / background | `agentLocation.type` 非 `local` | 排除 |
| 草稿 / 归档 / spec / project / best-of-N / 子会话 | 对应 bool 列或 value 字段 | 排除 |
| 无名历史会话 | 有 `composerData:<id>` 记录但 `name` 缺失、0 行改动 | **保留**为 `Untitled Session`，状态通常 Unknown（诚实退化）；本机 29 行中有 14 行属此类 |
| 经典 IDE 布局（`workspaceStorage/*/state.vscdb` 的 `composer.composerData`） | 28 个 composerId 与 `composerHeaders` **交集为 0** | 不实现：没有证据表明这些会话可被 `/agent?id=` 选中 |
| Cursor CLI / Cloud Agent / 远程机器 / 其他 profile | 方案已排除 | 不展示 |

## 6. 点击与回执

- 三次有效点击都精确选中了目标会话（同项目 A/B、跨项目 C）。
- **没有可用的选中回执**：Agents 窗口的选择不会写回经典布局的 `selectedComposerIds` / `lastFocusedComposerIds`（点击前后这两个键完全不变）。因此 Spark 只能确认「URL 已派发」，不能确认「已选中」；这直接影响实现：点击成功后不修改未读、不写数据库、不显示成功提示，只在 `open` 返回错误时记录并限频。
- 无效 ID 由 Cursor 自己报错，`open` 仍返回 0：**系统 opener 的退出码不能当作选中结果**。

## 7. 支持范围与已知限制

支持：

- macOS 稳定版 Cursor 3.24.12 / commit `37b86594…`：现代 Agents 本地会话的列表、运行/结束/未读/未知状态、`cursor://anysphere.cursor-deeplink/agent?id=<composerId>` 精确跳转。
- 其他版本：仍可只读展示已识别 schema 的会话，但状态按证据降级，跳转默认关闭，需 `SPARK_CURSOR_JUMP=1` 显式开启（未验证、仅用于人工验证）。

限制：

- 经典 IDE 历史会话不展示、不跳转（§5）。
- 没有选中回执（§6）。
- 冷启动与 Agents 窗口关闭两种点击情形未单独验证（§2 第 4 项）。
- 未验证等待审批 / 等待提问：`hasBlockingPendingActions`、`hasPendingPlan` 未使用，第一版不产生 Waiting。
- 未观察到 `sessionEnd`、`aborted`、`postToolUseFailure`；对应分支只有合成 fixture 覆盖。
- 冷启动时没有事件的旧会话显示 Unknown，这是有意为之。
- 会话行只显示 header 标题；标题为空的旧会话无法从正文补名（不读正文）。

## 8. 本次对机器的改动（可回滚）

| 路径 | 内容 |
| --- | --- |
| `~/.cursor/hooks.json` | 合并 8 个事件（`sessionStart`、`beforeSubmitPrompt`、`postToolUse`、`postToolUseFailure`、`afterAgentThought`、`afterAgentResponse`、`stop`、`sessionEnd`），每个事件追加一条调用 Spark helper 的条目；原有 16 个事件的其它工具条目全部保留 |
| `~/.cursor/hooks.json.spark-backup` | 合并前的原字节 |
| `~/.config/spark/bin/spark-cursor-hook` | helper 副本（0755） |
| `~/.config/spark/cursor/install.json` | `{"protocol":1,"collector_version":1,…}` |
| `~/.config/spark/cursor/events/<YYYY-MM-DD>/*.json` | 白名单事件，7 天保留 |

回滚：

```bash
# 只删 Spark 自己的条目、helper 与安装元数据，其它 hook 保持不动
/Applications/Spark.app/Contents/MacOS/Spark --cursor-uninstall
```

或手工把 `hooks.json.spark-backup` 覆盖回 `~/.cursor/hooks.json`（会丢掉此后其它工具的改动，仅在明确知道后果时使用）。

## 9. 测试命令与结果

```bash
go test ./internal/monitor            # 168 passed，1 failed（见下）
go test .                             # 63 passed
CGO_ENABLED=0 go test ./...           # 同上，纯 Go 驱动在无 CGO 下工作
go test -race . ./internal/monitor    # 无 data race
go tool mygo vet .                    # clean
go vet ./...                          # clean
SPARK_REAL=1 go test -run TestRealHomeRenders .   # 本机真实数据渲染：6 个项目 / 30 个会话（含 18 个 Cursor 会话）
CGO_ENABLED=0 GOARCH=amd64 go build . && CGO_ENABLED=0 GOARCH=arm64 go build .   # 两种发布架构均可构建
go tool mygo build -skip-dmg          # 发布构建通过，并用产出的 Spark.app 执行 --cursor-install 验证
```

唯一失败的是既有用例 `TestResumedRolloutStaysOneThread`（Codex 分段 rollout 的状态），与本次改动无关：在未含本次改动的基线上用 `git stash` 复现同样失败。本次未修改 Codex 逻辑，也未修改该用例。

Cursor 相关覆盖（`internal/monitor` 与根包合计 98 个用例）对应方案 §12.1 的每一行：

- collector：子进程不启动窗口（进程自行退出）、空 stdout、合法输入落盘、损坏/超限/IO 失败不影响 Agent、并发写不覆盖、不落正文。
- 安装合并：其它顶层键/事件保留、重复安装幂等、损坏与未知版本不覆盖、空格与单引号路径、旧 helper 更新、外部改动被检测、卸载只删自有项、两个实例并发安装串行化。
- SQLite：不创建输入库、现代 schema、缺表/缺列、坏行与可选字段缺失、WAL 实际可读、锁超时、库被整体替换后重读、带空格路径。
- 发现与关联：同项目两会话、跨项目、标题缺失、多根工作区、同名 ID 跨来源不合并、仅 hook 的会话不入列表、归档/草稿/云端/子会话过滤。
- reducer：提交候选不冒充运行、工具与纯文本活动、完成/中止/错误、未读变化、旧读取+新 stop+库被锁、stop 后元数据未更新、旧 generation 的 stop、无 generation 的 stop、stop 后活动、后续新轮、重复终态、矛盾终态、同毫秒冲突、超时 Unknown。
- 跳转：固定 URL 与参数编码、非法 ID、未验证版本不启用、来源正确分发、不开真实窗口、不标已读。
- UI：Unknown 行与项目汇总、可跳/不可跳 hover 与光标、非空列表安装、安装失败/成功/未上报/文件损坏、窄窗口、真实数据多帧渲染。

## 10. 相对方案的实现偏差

| 方案 | 实际实现 | 原因 |
| --- | --- | --- |
| 未验证版本一律不启用跳转，`SPARK_CURSOR_JUMP` 只是临时开关 | P0 通过后把 `3.24.12 / 37b86594…` 写入 `cursorVerifiedBuilds`，该构建默认启用跳转；其它构建仍需 `SPARK_CURSOR_JUMP=1` | 方案 §4.2 把 P0 定为默认启用的门槛，验证在 §2/§4 已完成。如果不想要这个默认值，删掉 `internal/monitor/cursorcompat.go` 里那一条即可，代码会回到「全部关闭 + 显式 opt-in」 |
| 关联条件为「header 的 revision/UpdatedAt 在终态之后」 | 改为「元数据在终态之后被读取，且留 2 秒落定」 | §4：3.24.12 的 header 时间戳在轮次开始时写入，永远不会晚于 stop，按原文实现会让结束的轮次永远是 Unknown |
| 慢安装放 worker，结果经 `Window.Update` 回来 | 安装同步在点击回调里执行 | 实测复制 23 MB 二进制 4.5 ms（发布包 14.6 MB 更快），没有值得跨线程的耗时；并发点击由文件锁与幂等合并兜住 |
| 读取失败超过 30 秒「停止基于缓存清除未读或确认跳转资格」 | 超过 30 秒后仍用快照的标题/项目作为标签，但状态回到 Unknown、跳转关闭 | 只是让列表不突然消失；可用性与证据等级仍然按 stale 处理 |
| 快照/DB 变化靠 size/mtime 缓存键失效 | 每次扫描都重新读库（本机 16–29 行约 6 ms），只对锁失败保留快照 | 读一次比维护缓存键更简单，也天然满足「至少每 5 秒强制探测」 |
