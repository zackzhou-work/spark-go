# Cursor 本地 Agent 接入技术方案

状态：可交付实现；先完成 P0 验证，再实现与发布。  
适用仓库：`github.com/zackzhou-work/spark-go`。  
事实依据：[调研报告](../research/cursor-local-agent-integration.md)。

## 1. 目标与已经确定的范围

让 Spark 展示 Cursor 桌面本地 Agent 会话，持续更新状态，并能点击某条记录精确打开对应会话。

已确定的产品约束：

- 仅覆盖桌面本地 Agent；Cursor CLI、Cloud Agent、远程机器和其他 Cursor profile 不在第一版范围内。
- 接受进入 Cursor 独立 Agents / Glass 窗口，前提是精确选中对应会话。
- 采用官方 Hooks 与只读本地会话元数据组合。
- 接受验证并使用未公开承诺的内置会话链接。
- 第一版优先准确表达运行、结束、未读；不承诺准确识别等待审批、模型提问及其原因。
- 已有 Claude、Codex、pi 的扫描、状态和跳转保持现有行为。

本方案不授权 Agent 通过提交新 prompt、恢复 CLI 任务或创建会话来模拟跳转。受控人工测试若需要合成会话，应单独说明并取得该项操作授权。读取标题与结构化元数据即可，不读取 transcript 或保存 prompt、工具内容、回答正文。

## 2. 核心结论与实现门槛

监听有官方 Hooks。精确跳转存在随包静态证据，但尚无运行验收。

候选链接：

```text
cursor://anysphere.cursor-deeplink/agent?id=<URL编码后的composerId>
```

本机观察基线为 Cursor **3.24.12**，commit `37b865941ccb31e2fc90c685157534efca87f960`。随包链路为：

```text
cursor scheme
→ anysphere.cursor-deeplink /agent?id=...
→ cursor.openOrFocusGlassWindow
→ Glass handleCursorDeeplink → openAgent(id)
→ glass.openCloudAgentById
→ getAgentHeader(id)
→ selectAgentRequested
```

已有本地 header 可以进入选择逻辑；缺少 header 的本地 UUID 不会自动补载。链接进入 Agents 窗口，不能保证任意经典 IDE 历史聊天都可打开。

**P0 是实现门槛：**先验证两条同项目本地会话 A/B 的 ID 关联与精确打开，再推进默认启用的跳转功能。若不通过，提交证据与阻塞原因；不要把打开项目、激活 Cursor、使用 CLI resume 或扩展桥接当作同一方案的自动替代。

静态证据与运行结果冲突时，以运行结果修正兼容范围，并同步本方案和测试。其他 Cursor 版本初始状态为未验证，不应因为版本号更大就默认支持所有私有能力。

## 3. 总体结构

保持现有 `Scanner → []ProjectGroup → Window.Update → view` 路径。Cursor 私有格式、数据关联、兼容策略集中在 `internal/monitor`，UI 只接收标准会话与安装状态。

```text
Cursor 用户级 hooks
  → Spark --cursor-hook（无 GUI 的短命进程）
  → 白名单事件文件
                             ↘
Cursor SQLite（只读） → Cursor scanner → 会话归并/状态决策
                                            ↓
                                      Scanner.ScanAll
                                            ↓
                                 现有分组、角色与列表
                                            ↓ 点击
                              CursorDeeplink → MyGo URL service
```

不引入通用 provider framework、常驻本地 HTTP 服务或跨来源状态引擎。SQLite adapter、事件读取与纯状态 reducer 可以有私有测试接口；不要让 view 知道 SQLite 字段或 generation 规则。

### 3.1 建议文件划分

| 文件 | 职责 |
| --- | --- |
| `internal/monitor/cursor.go` | Cursor 路径、扫描、会话关联、项目与时间归并 |
| `internal/monitor/cursordb.go` | 只读 SQLite、schema 探测、header 与 workspace adapter |
| `internal/monitor/cursorevents.go` | 输入筛选、事件落盘、增量读取、保留策略 |
| `internal/monitor/cursorstate.go` | 纯状态 reducer 与数据新鲜度判断 |
| `internal/monitor/cursorinstall.go` | helper 安装、hooks 合并、安装状态、移除自有条目 |
| `internal/monitor/cursorcompat.go` | 已验证版本/数据布局/跳转覆盖范围 |
| 对应 `_test.go` | 合成 payload、临时 SQLite fixture 与集成测试 |
| `main.go` | 早期 helper 分支、Cursor side state |
| `internal/monitor/state.go` | Cursor 来源、Unknown 状态、可跳转标记 |
| `internal/monitor/scanner.go` | Cursor 扫描接入与有限 watch 目录 |
| `internal/monitor/deeplink.go` | Cursor URL 构造 |
| `view.go`、UI 测试 | 安装入口、来源文案、未知状态、点击分发 |
| `README.md` | 支持范围、安装/卸载、状态含义、已验证版本 |

文件名可随实现合理合并；不要把数据库、事件并发和安装逻辑全部塞进 scanner。

## 4. P0：先验证协议和跳转

P0 可以只交付验证记录与极小诊断代码，不需要先完成产品 UI。

### 4.1 验证清单

1. 记录 Cursor 版本、commit、macOS、默认 URL handler 是否指向该安装。
2. 使用用户指定的测试会话，确认 `hook.conversation_id == composerHeaders.composerId`；不能通过标题相近猜关联。
3. 同项目两条本地会话 A/B：分别调用 URL，确认实际 UI 选中对应 ID 的聊天，而非仅窗口激活。
4. 跨项目、Agents 窗口已打开/关闭、Cursor 冷启动、已结束会话各测试一次。
5. 经典 IDE 旧会话单独测试并记录是否进入 Agents repository；失败的类型不得标为默认支持。
6. 无效 ID 不创建新聊天、不提交任务；记录 Cursor 的错误表现。
7. 采集一轮提交、生成、stop、取消的 hook 事件名与必要字段；只保存白名单化后的记录。
8. 确认 headers 中标题、工作区、未读、归档、草稿、本地/云端/子会话的实际字段形状与枚举值。已有调研只有字段存在证据，未验证完整枚举。

输出 `docs/research/cursor-integration-validation.md`，逐项写明通过/失败/未执行与版本。截图可打码，不能把真实聊天正文纳入 fixture。

### 4.2 通过标准

- A/B 点击确实进入各自会话，不能只记录 `open` 返回成功。
- 至少一轮事件与 header 的 ID 一致，能够区分两个并行会话。
- 明确哪些 header 类型可归为本地桌面会话、哪些可跳转。
- 未读字段与 UI 的变化对应得到验证。

不能运行本机 GUI 的实现 Agent 可以完成离线实现，但必须将 P0 标为未完成，跳转保持关闭，不宣称整体验收通过。

## 5. 数据目录与协议

### 5.1 输入路径

```text
用户 hooks：~/.cursor/hooks.json
全局库：~/Library/Application Support/Cursor/User/globalStorage/state.vscdb
工作区库：~/Library/Application Support/Cursor/User/workspaceStorage/<id>/state.vscdb
```

第一版仅默认稳定版 profile。不自动搜索 `Cursor - Insiders`、其他用户、备份或远程目录。测试通过注入路径使用临时目录，不依赖真实 HOME。

### 5.2 Spark 自有路径

```text
~/.config/spark/bin/spark-cursor-hook
~/.config/spark/cursor/install.json
~/.config/spark/cursor/events/<YYYY-MM-DD>/<eventId>.json
```

目录 `0700`，事件和安装元数据 `0600`，helper `0755`。UUID 不直接拼接成路径；事件名由 Spark 生成，避免 Cursor 输入影响输出目录。

使用每事件一个不可变文件，临时文件写完后在同一目录原子 rename。这样并发 hook 不会互相覆盖“最近会话状态”，也不需要跨进程读改写同一个 JSON。

### 5.3 白名单事件结构

以下是 **Spark 内部 schema**，不是 Cursor 原生 payload：

```json
{
  "schema": 1,
  "event_id": "<Spark生成的随机ID>",
  "collector_version": 1,
  "received_at_ms": 1790000000000,
  "conversation_id": "<Cursor原值>",
  "generation_id": "<可选>",
  "event": "stop",
  "workspace_roots": ["/absolute/project"],
  "cursor_version": "3.24.12",
  "is_background_agent": false,
  "status": "completed",
  "reason": "<可选的已知枚举>"
}
```

- `received_at_ms` 在 helper 入口立即采集，代表本机接收时间，**不是 Cursor 源事件时间或可靠顺序号**。
- `conversation_id` 必填；缺失/非法则丢弃，不关联到最近会话。generation 缺失时降低证据等级，不能结束另一已知 generation。
- 限制 ID 长度为 128 字节；本地 ID 格式以 P0 实际 UUID 规则为准。generation 只作不透明关联键，不能按字典序排序。
- 只接受安装时声明的事件及已知 status/reason 枚举；新字段可忽略，未知状态不能映射成成功。
- 不保存完整 stdin、prompt、attachments、tool input/output、正文、邮箱、transcript 路径、自由文本错误。
- 非法 UTF-8、超长路径、重复根目录需处理；保留多根数组，最多 32 项，单项最多 4096 字节。
- hook 输入最大 8 MiB；先限制读取再解码。超过上限、损坏或落盘失败时放弃本事件，不能输出阻断决策。

事件保留 7 天；清理最多每小时一次，只删除 Spark 自有目录内过期的普通文件。启动时只回放保留窗口内事件；之后按文件名集合增量读取。每次扫描处理上限 2000 个新文件，剩余下次继续；不得为每次 UI rebuild 重放事件。记录 backlog 指标，测试长队列恢复。不要凭任意保留条数截断而丢掉最新 turn 的终态。

这不是恰好一次消息系统；reducer 必须幂等处理重复事件，不能用随机 event ID 掩盖语义重复。

## 6. Hook 安装与执行

### 6.1 使用 Spark 自身的无 GUI 分支

第一版避免新增 Python、jq、Node 或 shell JSON 解析依赖。复用当前架构的 Spark 可执行文件，增加：

```text
Spark --cursor-hook
```

`main()` 进入任何 scanner、窗口、App.Run 或普通 flag 流程之前识别该参数，调用 collector 并退出。仅该模式读取 stdin。collector 必须通过子进程测试证明不会创建窗口、扫描用户会话或依赖 app bundle 资源。

安装时将当前稳定构建的 executable 原子复制到 `~/.config/spark/bin/spark-cursor-hook`，hooks 命令引用此固定路径并附带 `--cursor-hook`。因此 Spark.app 移动或退出后 hook 仍能写事件。不要让 hooks 指向 `go run` 临时路径。开发构建可手动安装，复制到固定路径后同样验证。

复制的是用户明确点击安装时的当前架构二进制；升级由安装状态判断后展示 Update。不要每次 scan 复制或 hash 整个可执行文件：构建标识/协议版本用于常规判断，完整一致性校验只在安装或明确诊断时做。

如果 MyGo 的包初始化导致该分支仍触发 GUI 或依赖 bundle，P1 必须改为独立纯 Go helper，并将 helper 纳入发布构建；不能接受“通常不弹窗”。这项必须早于正式安装功能验收。

### 6.2 事件订阅

建议初始事件集：

```text
sessionStart
beforeSubmitPrompt
preToolUse
postToolUse
postToolUseFailure
afterAgentThought
afterAgentResponse
stop
sessionEnd
```

理由：建立轮次候选、观察实际活动、确定本轮与会话结束。afterAgentResponse/Thought 仅表示活动，不作为整轮完成。可依据 P0 精简高频事件，但精简后须验证纯文本、无工具长轮次不会直接失联。

以实施时的 [官方 Hooks 文档](https://cursor.com/docs/agent/hooks) 确认精确配置键与超时单位，不能照搬 Codex 的嵌套 hooks/matcher schema。配置形状示意：

```json
{
  "version": 1,
  "hooks": {
    "stop": [
      {"command": "'<absolute-helper-path>' --cursor-hook"}
    ]
  }
}
```

这只是一个事件的示例，真实配置需包含选定事件。路径做正确 shell quoting，覆盖空格和单引号，不能用 JSON.stringify 代替 shell escaping。

collector 成功或失败均不返回 `permissionDecision`、`continue`、`followup_message` 等控制字段。默认空 stdout、exit 0；P0/P1 按目标版本验证空输出被视为观察成功。若某事件要求 JSON 响应，按官方该事件契约输出中性响应，不能发明跨事件通用字段。

### 6.3 幂等合并与安装状态

安装模块公开小接口，名称可调整：

```go
func InstallCursorHooks(home, executable string) error
func CursorHooksOnDisk(home string) CursorHookState
func RemoveCursorHooks(home string) error
```

`CursorHookState` 至少为 Missing、Current、Outdated、Broken。安装流程：

1. 读取并解析现有 JSON，保留未知顶层键、其他事件及其他 hook entries。
2. 损坏文件报错并保留原文件；未知 version 不擅自迁移。
3. 仅识别并替换引用 Spark 固定 helper 路径和参数的自有条目；不能用包含 `spark` 的模糊匹配。
4. 安装前保存原字节的可追溯备份；写入前复查原文件未被用户改动，变化则重新合并或报冲突。
5. 先原子安装可执行 helper，再原子替换配置；失败保留旧 helper 或恢复，避免新配置指向不存在文件。
6. 写 `install.json`，记录协议/collector 版本和安装时间。不能把“配置有条目”当作“Cursor 已加载并成功上报”。

两个 Spark 实例的并发安装需互斥。外部编辑器不遵守锁，复查只能缩小竞态；不要宣称文件合并完全无竞态。相同内容安装两次不得产生重复条目。

卸载仅删除 Spark entries、自己的 helper 和 install 元数据，不回滚整个备份覆盖用户后续修改；事件目录可保留到期清理。正常启动不自动安装或改写用户配置。

## 7. SQLite adapter 与会话发现

### 7.1 驱动与访问

使用 `database/sql` 加纯 Go SQLite 驱动，建议 `modernc.org/sqlite`。实现时选择兼容仓库 Go 1.27.1 的明确版本，提交 go.mod/go.sum，并验证 `CGO_ENABLED=0`。不调用用户环境中的 sqlite3 命令，不执行迁移，不创建输入库。

以 driver 支持的 read-only URI 打开，正常读取 WAL；不要使用 `immutable=1` 读取正在写入的 Cursor 库，不设置 journal_mode 或做 checkpoint。连接数 1，busy timeout 约 100ms，单次读取 context deadline 250ms。输入不存在时返回 unavailable，不创建空数据库。

schema 通过 sqlite_master/PRAGMA table_info 探测，列名来自内部 adapter 的固定白名单。连接与缓存由 scanner goroutine 持有；明确关闭时机，重复打开失败不能积累 handle。不要从 UI callback 并行访问同一个 scanner。

### 7.2 首选现代 header 布局

本机已观察到：

```text
composerHeaders(
  composerId TEXT PRIMARY KEY,
  workspaceId TEXT,
  createdAt INTEGER,
  lastUpdatedAt INTEGER,
  isArchived INTEGER,
  isSubagent INTEGER,
  recency INTEGER,
  checkpointAt INTEGER,
  subagentTypeName TEXT,
  value TEXT
)
```

JSON value 中已见 `name`、`hasUnreadMessages`、`isDraft`、`workspaceIdentifier`、`agentLocation` 等可选字段。

优先 SQL 过滤 archived/subagent，随后白名单解析 value。`name`、unread、本地类型均用可选值表达，不能把缺字段变成 false。字段类型错只影响该行；记录限频兼容错误。限制单个 header JSON 为 1 MiB，单行异常不能阻断其他行。

`workspaceIdentifier` 与 `agentLocation` 的实际结构及枚举尚待 P0 核实：把已验证结构固化为 fixture，再写 parser。禁止按字段名字猜出完整 JSON 类型并把它当稳定协议。

### 7.3 经典 IDE 布局与支持范围

已观察到 workspace `ItemTable['composer.composerData']` 含 `allComposers`、`selectedComposerIds`、`lastFocusedComposerIds`。这可以作为经典 IDE adapter 的依据，但只有 P0 明确关联与跳转覆盖后才默认启用。

第一版必须完整支持经验证的现代 Agents 本地 header；经典布局无法验证时明确标为 unsupported，不读完整 `composerData:<id>` 会话正文去补齐。不得把“没有现代表”简单解释成“用户没有会话”。

不按全局库包含记录就纳入：排除 cloud/background、draft、archived、subagent、内部 project/spec 记录的精确规则由 P0 fixture 确定。未知类型保守排除并记录结构化原因。Hooks 来自 GUI/CLI 的公共字段不足以严格区分来源，因此仅 hook、未关联到已验证桌面本地 metadata 的 ID 不展示为桌面会话；之后 metadata 到达再补上。

### 7.4 标准化模型

私有模型示意：

```go
type cursorHeader struct {
    ID              string
    Title           string
    WorkspaceRoots  []string
    CreatedAt       time.Time
    UpdatedAt       time.Time
    Unread          *bool
    LocalDesktop    bool
    JumpEligible    bool
}
```

保留可选语义，不以零值掩盖缺失。按 SourceCursor + ID 去重；与其他来源相同字符串的 ID 不合并。还要检查 UI 的 key、dismiss 状态 map 是否只用了 ID，必要时使用来源与 ID 的组合键。

标题使用 header name，缺失为 `Untitled Session`；不读取第一条 prompt 补标题。

工作区优先使用已验证 metadata 路径；hook roots 只补缺项，不覆盖已验证工作区归属。多根用第一个规范化非空 root 作为第一版主分组路径，同时保留完整数组供关联；明确这是显示规则。使用 filepath.Clean，不必 EvalSymlinks。WorkingDir 为主 root，ProjectName 使用现有 basename 分组规则；同名项目合并问题不在本次重构。

ActivityAt 为 header UpdatedAt 和可信活动事件时间的较新者；不能用数据库 mtime、安装时间、轮询时间代替会话活动。无法取得时间则使用 CreatedAt，再缺则零值并归 Earlier；不要归 Today。时间解析复用仓库现有工具。

## 8. 状态归并

### 8.1 显式表达未知

在 TaskState 末尾追加 `Unknown`，保留已有枚举值，不改现有来源语义。Cursor 不足以判定运行/结束时使用 Unknown，不能返回 Completed 伪装“已经看过”。

必须处理 `String()`、priority、排序、行样式、projectLook、projectCard.status 和角色汇总。Unknown 灰色静态标记，文案 `Status unavailable`，无 spinner、New、Needs you。项目只有 Unknown 或含 Completed+Unknown 且无更高优先级状态时不能显示 `All caught up`，改为 `Status unavailable`；角色可复用中性静态姿态。顺序为 Running/Waiting/Unread、Unknown、Completed。原有来源不会主动产生 Unknown。

Cursor 第一版不返回 Waiting，也不猜 WaitPermission/WaitQuestion。

### 8.2 reducer 输入与输出

使用可注入 now 的纯函数处理已标准化 header、事件与兼容状态；数据库和文件读取不进入 reducer。至少保留：当前 generation、最后可信活动、最后终态、状态是否确定、可选终态原因和 evidence source。内部可用 reason 枚举，但不要把诊断细节塞进主列表。

| 输入证据 | 输出 |
| --- | --- |
| 当前 generation 存在可信实际活动，之后无匹配终态 | Running，受新鲜度策略限制 |
| 只有 sessionStart | 不证明生成，Unknown，或使用同轮其他有效证据 |
| 只有 beforeSubmitPrompt | 提交候选；其他 hook 可能拒绝，不能直接持久 Running |
| 当前轮 stop completed/aborted/error | 本轮结束；有可靠 unread=true 则 Unread，否则 unread=false 则 Completed，缺 unread 为 Unknown |
| sessionEnd 且无更晚的新轮活动 | 终止当前会话活动；按同样 unread 规则展示 |
| 只知道 unread=true，没有可信正在运行证据 | Unread |
| 只知道 unread=false，没有结束证据 | Unknown，不能凭已读推断没运行 |
| 无可靠运行/结束/未读证据、schema 不支持、长期陈旧活动 | Unknown |

`Completed` 在现有 UI 代表“结束且已读”，不是“任务执行成功”。aborted/error 可保留内部终态原因；如展示详情必须使用准确文案，不写 success。

表中的 unread 必须能够关联到当前轮的结果，而非随便一份 header。adapter 记录成功读取时间与元数据 revision/UpdatedAt；终态到达前的缓存不能用来确认新轮 Completed 或清除新轮未读。仅仅在 stop 后重新查询到旧值也不足以证明已持久化本轮结果：P0 必须验证 UpdatedAt/revision 与轮次完成及已读变化的关系，确定可用的关联条件。满足已验证条件的当前结果 unread=true 才确认 Unread，false 才确认 Completed；否则维持 Unknown，等待可关联的元数据。若 P0 无法建立该条件，不实现自动 Completed 推断。既有静态未读记录可以独立展示 Unread，但不能把上一轮的 false 用于新轮。

仅靠目前的 header 字段，Spark 冷启动面对未上报的旧会话可能显示 Unknown，这是第一版的诚实退化。若 P0 验证到可靠实时状态字段，可以增加 adapter 证据，不能直接用未核实的 `status` 枚举。

### 8.3 generation、重复与乱序

- 不按 generation ID 字符串排序。
- 用 beforeSubmitPrompt 建立 generation 候选，用同 generation 的后续活动确认。一个 session 的不同 generation 分别记录，旧 generation 的 stop 不能结束新 generation。
- 没有 generation 的 stop 不能结束已有不同 generation 的 Running；降为不充分证据。
- 同 generation 内接收时间只用于诊断与新鲜度；明确无冲突的活动/终态可归并，但“终态后收到活动”无法区分延迟启动的 hook 与真实续跑，一律先降为 Unknown。不能仅按接收较晚就重新进入 Running；同毫秒冲突也不得通过 eventId 字典序猜语义。
- afterAgentResponse/Thought 只表示活动，不表示结束。
- `stop` hook 可被其他 hook 的 followup_message 触发后续运行；必须在 P0 核实实际 generation 是否变化。新 generation 的已关联活动可以恢复 Running；同 generation 的续跑只有存在 P0 验证过的额外顺序/实时证据才恢复 Running，否则保持 Unknown，直至新的可靠终态或新轮证据。
- 缺少 beforeSubmitPrompt 的 generation 不能无条件取代另一个活跃 generation；证据冲突时 Unknown，等待后续可关联事件。
- 重复同语义事件幂等，不制造新轮、额外未读或活动时间跳动。

只有本机接收时间，没有源 sequence，所以不能完全纠正任意延迟/乱序。对无法证明的顺序保守降级，不宣称绝对正确。测试必须包含旧 stop 晚到、缺 start、新轮接收更早、同毫秒冲突、重复终态、延迟活动晚于 stop，以及同 generation 的真实续跑；后两者没有额外可区分证据时都应 Unknown。

### 8.4 新鲜度与失联

初始默认活动有效期 30 分钟，采用可测试常量。不是“30 分钟后任务完成”：过期且没有可靠终态则 Unknown。长工具期间缺事件也可能过期，需在 README 写明；P0/P2 如找到可靠心跳可优化。

不能用 Cursor 进程存活证明每条会话 Running。进程退出可以使未闭合活动失去实时可信度，但不能改成任务成功。暂时 DB 锁失败时保留上一 header 快照最多 30 秒；超过后元数据视为陈旧，停止基于缓存清除未读或确认跳转资格。下次成功恢复。

## 9. 扫描、缓存与线程

`NewScanner()` 注入默认 Cursor 路径，测试可覆盖；`ScanAll()` 在现有来源之后追加 `scanCursor(now)`，沿用 groupByProject。

只 watch Spark cursor events 目录与 Cursor globalStorage 目录；不要递归监视整个 Cursor User 或所有 workspaceStorage。工作区 adapter 如启用，沿用 fallback 定时发现有限数据库文件。

现有 watcher debounce 300ms、正常 fallback 5 秒、watch 不可用时 2 秒，可直接复用。数据库 WAL 变化和目录原子替换都需要触发重读；不能只看 state.vscdb mtime。缓存失效键包含主库与 WAL 的 size/mtime，并至少每 5 秒强制探测一次，避免 stat 未变导致永久错过更新。

数据库、事件增量读取和安装状态检查在后台 scan 上执行。view 构建不可读库/文件。慢安装工作在 worker 执行，UI 通过 `Window.Update` 接收结果；防重复点击并在窗口关闭后丢弃结果。

遵循仓库 MyGo 0.3.5 的技能要求：实现前读取 mygo-maintenance；不能把 ui.Context 或 Element 捕获到 goroutine。需异步 URL 回调时使用当前版本支持的 services，或沿用现有 `openURL` 的合法调用方式。

## 10. 跳转与兼容状态

### 10.1 数据接口

在 SessionSource 末尾加 SourceCursor，String 返回 `Cursor`。建议给 SessionItem 增加 `CanJumpCursor bool`，只由 scanner 根据已验证能力及当前 header 计算；该字段用于 `Jumpable()` 的 Cursor 分支，不能让 default=true 覆盖未知 Cursor 记录。

```go
func CursorDeeplink(id string) (string, bool)
```

仅允许 P0 验证的本地 ID 格式；通过 net/url 构造，固定 scheme/authority/path，query 名为 `id`，不拼接外部 URL。输入非法时不调用系统 opener。

`view.go:jumpToSession` 改为显式按来源分发，Cursor 调用 CursorDeeplink；pi 保留 Herdr 路径。测试证明 Cursor 记录不会进入 Claude 分支。

### 10.2 启用条件与失败

`CanJumpCursor` 为 true 的条件：已验证版本/布局、桌面本地会话、有效 ID、符合 P0 跳转覆盖类别、存在新鲜 header。单纯有 composerId 不足以宣称所有旧会话可跳转。

未验证版本仍可在已识别 schema 下显示只读元数据，状态按证据降级；跳转初始关闭，显示简短兼容提示。兼容表以实际测试过的版本/commit 或明确布局 profile 为单位，不采用 `version >= 3.24.12` 的无限范围。

点击只发一次 URL。系统 opener 成功仅表示已派发，不证明选中成功；不据此修改 unread，不写 Cursor DB。系统调用错误展示非阻断提示并限频日志。Cursor 内部找不到 ID 时，Spark 在没有回执的条件下不承诺能自动检测。

禁止失败后自动创建聊天、预填 prompt、调用 CLI resume、回退打开其他会话或只打开目录。扩展桥接作为未来独立方案，重新评估后实施。

## 11. UI 与安装反馈

扩展 sideState/applySideState，至少包含 Cursor 安装状态与是否收到当前 collector 版本事件。安装状态与会话数据分开，事件到达即使 groups 未变也能刷新 setup 提示。

- 检测到 Cursor 默认安装或数据目录时才提供 `Install for Cursor`；未使用 Cursor 的用户不增加永久缺失提示。
- 已有会话时也必须能安装/更新，不依赖只在空列表显示的 banner。复用现有 pi setup notice 位置或同类轻量入口。
- Installed 仅表示配置与 helper 就绪；未收到事件时显示 `Cursor is set up. Start a new turn to verify reporting.`，不宣称实时监听已验证。
- 配置损坏显示简短错误；不覆盖用户文件。安装失败不把按钮状态改成 Current。
- 来源显示 Cursor，点击提示为 `Open in Cursor`。无跳转能力的记录无 hover 箭头、不显示手型，保留正常时间。
- Unknown 有明确行文案；不能变成 Completed 的空心圈或项目 `All caught up`。
- 保持现有窄窗口布局与键盘可用性，安装按钮超过可用宽度时换行。

## 12. 测试与验收

### 12.1 离线自动化

全部 fixture 人工构造，禁止复制真实聊天或真实 DB。

| 测试面 | 必须覆盖 |
| --- | --- |
| collector 子进程 | 不启动 GUI；空 stdout；合法输入落盘；损坏/超限输入、IO 失败不影响 Agent；并发写无覆盖；不落正文 |
| 安装合并 | 其他顶层键/事件保留；重复安装幂等；损坏/未知 version 不覆盖；空格和单引号路径；旧 helper 更新；原文件并发变化；卸载只删自有项 |
| SQLite | 只读不创建输入库；现代 schema；缺表/列；坏行/缺可选字段；WAL 实际可读；数据库锁/替换后恢复；连接释放 |
| 发现与关联 | 两会话/多项目；标题缺失；多根；重复 ID 跨来源；CLI hooks 不混入桌面列表；过滤归档/草稿/云端/子会话 |
| reducer | 提交候选不冒充运行；工具/纯文本活动；完成/中止/错误；未读变化；旧已读快照+新 stop+DB 锁住；stop 后查询仍是旧 revision；旧 generation stop；无 generation；乱序冲突；重复；后续 loop；超时 Unknown |
| 跳转 | 固定 URL 与参数编码；非法 ID；未知版本不启用；来源正确分发；不开真实窗口；不标已读 |
| UI | Unknown 行与项目汇总；可跳/不可跳 hover；非空列表安装；安装失败/成功/未上报；窄窗口与多帧生命周期 |

建议检查命令：

```bash
go test ./internal/monitor
go test ./...
CGO_ENABLED=0 go test ./...
go tool mygo vet .
```

如本次引入共享可变状态，再对相关包执行 race 检查。driver 加入后验证项目既有 Apple Silicon/Intel 发布构建流程；不要为文档或 fixture 变更反复运行无关压力测试。

### 12.2 端到端验收

1. 用户点击安装，旧 hooks 保留，Spark 退出时 collector 仍能记录。
2. 两条同项目本地会话均出现，标题/归属正确且不串状态。
3. 开始实际生成后进入 Running；完成后依据 Cursor 未读标记区分 Unread/Completed。
4. 在 Cursor 阅读后，Spark 随下一次成功扫描清除 New；仅在 Spark 点击链接不抢先清除。
5. 点击 A/B 分别打开正确会话；跨项目、冷启动覆盖结果与兼容声明一致。
6. 终止 Cursor、漏 stop、锁 DB、损坏一行或替换库均不崩溃、不永久假 Running、不误报成功。
7. 未安装 hooks 的旧会话可以显示 Unknown；UI 不把未知伪装成完成。
8. Claude/Codex/pi 既有测试与人工关键路径通过。

记录每项的实际版本、结果和限制。P0 未执行、关键测试失败或某来源被回归时不能声明完成。

## 13. 实施顺序与交付要求

### P0：验证与 fixture

验证 ID/会话类型/元数据枚举/双会话跳转，写验证记录与脱敏 fixture。失败时停在结论，不绕过门槛改变目标。

### P1：collector 与安装

完成无 GUI 模式、白名单事件、原子写入、合并安装/卸载与子进程测试。先证明 helper 从固定路径独立运行，再做安装按钮。

### P2：数据库与状态

加入纯 Go SQLite driver、现代 adapter、schema/版本探测、事件 reducer、增量缓存、Unknown。通过临时库与合成事件测试后接入 ScanAll。

### P3：UI 与跳转

接入安装状态、Cursor 来源、Unknown、jumpability 和 URL 分发。只为 P0 通过的覆盖范围启用。补 UI 多帧测试与来源回归。

### P4：发布检查

跑必要检查与端到端矩阵，更新 README 与验证记录，确认 helper/依赖在发布包架构下可用，写明尚未覆盖的旧 IDE 布局与 Cursor 版本。

建议同一工作树只由一个 Agent 写代码；独立 reviewer 可以并行只读审查协议、状态和数据访问。不要让多个 Agent 同时修改 main.go、state.go、scanner.go、view.go。

最终交付必须包含：实现 diff、验证记录、测试命令及结果、支持版本与会话范围、已知限制、安装和卸载说明。若仅离线实现完成，明确写“P0/GUI 验收未完成，私有跳转未默认启用”。

## 14. 可直接转交给实现 Agent 的任务

> 按 `docs/plans/cursor-local-agent-integration.md` 为 Spark 接入 Cursor 桌面本地 Agent。先阅读仓库规范、mygo-maintenance 技能、调研报告与本方案，完成 P0 双会话精确跳转和字段关联验证，随后按 P1–P4 实现。优先现代 Agents 会话，使用官方 hooks + 只读 SQLite 元数据；第一版运行/结束/未读，证据不足显示 Unknown。用户接受进入 Agents 窗口，接受内置私有链接，尚未授权替换为扩展桥接。不要把系统成功打开 URL 当作选中成功，不读取聊天正文、不修改 Cursor 数据库，不回归现有来源。交付代码、测试、验证记录、支持范围和残余限制。无法进行 GUI 验证时可以完成离线部分，但保留跳转门槛并明确未完成项。
