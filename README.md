# spark

监控 Claude Desktop（Code 标签页）里各个会话的任务状态的 macOS 小窗。用 Go 和 [mygo](https://github.com/egoist/mygo) 的原生 UI 写成。

## 状态来源

- **进程**：每个 claude 核心进程的环境变量 `CLAUDE_CODE_HOST_SESSION_ID` 对应桌面端的会话 ID，没有进程就是回合已结束。
- **transcript**：读 `~/.claude/projects/<cwd>/<cliSessionId>.jsonl` 末尾。end_turn 或用户打断为回合结束；工具调用挂起为 Running；AskUserQuestion / ExitPlanMode 为 Waiting。
- **hooks（可选）**：装上后"等待授权"由 Claude Code 直接报告，否则只能靠"工具挂起 45 秒无动静"推测。
- **会话 JSON 的 `lastFocusedAt`**：回合结束后，`lastActivityAt` 比它新就是产出还没在桌面端看过，算 Unread。

四种状态和桌面端侧栏那颗点一一对应：

| spark | 桌面端侧栏 | 含义 |
| --- | --- | --- |
| 灰色呼吸 | 实心深点 | 回合进行中 |
| 红点 | （仍是实心深点） | 卡在授权框或模型的提问上 |
| 黄点 | 黄点 | 回合结束，产出还没看过 |
| 空心灰圈 | 空心圈 | 回合结束且已经看过 |

侧栏对"等授权"不单独标色，这一档是 spark 多出来的信息，所以给了红色而不是黄色。

扫描由文件监听驱动（会话目录、transcript 目录、hooks 目录），另有 5 秒定时兜底处理进程退出等无文件事件的变化。

## 点击跳转

单击任意一行（包括已完成的会话），Claude Desktop 会切到对应的 Code 会话并置前，用的是它自己的私有深链：

```
claude://code/continue?session=<sessionId>&source=spark
```

`sessionId` 就是会话 JSON 里的那个 `local_<uuid>`，不需要任何映射。窗口的唤起和置前由 Claude Desktop 自己完成，spark 只负责打开这条 URL。

**这条链没有公开契约**，是从 Claude.app 2.2553.1 的 `app.asar` 路由代码里读出来的，随时可能变：

- 应用侧的校验正则是 `^local_[A-Za-z0-9-]{1,64}$`，`internal/monitor/deeplink_test.go` 钉住了同一套规则。会话 ID 对不上格式时不会发出请求，只打一行 `[Jump]` 日志——那通常就是格式变了的信号。
- 深链整体受一个远端开关和 `~/Library/Application Support/Claude/config.json` 里的 `disableDeepLinks` 管控，被关掉时点击不会有任何反应。
- 如果哪天 `code/continue` 不灵了，还有一条 `claude://resume?session=<cliSessionId>`（裸 UUID）可以试。它走的是"导入 CLI 会话"的路径，可能产生重复会话，所以没有拿来做自动兜底。

**多账号未处理**：spark 会扫描所有 `<账号>/<组织>` 的会话，而深链只能命中当前登录账号的那些。

## 安装 hooks

先把脚本复制到固定位置，再把下面内容合并进 `~/.claude/settings.json`。脚本只把事件写到 `~/.config/spark/hooks/<session_id>.json`，不影响 Claude Code 的任何决策。

```bash
mkdir -p ~/.config/spark && cp hooks/spark-hook.sh ~/.config/spark/spark-hook.sh && chmod +x ~/.config/spark/spark-hook.sh
```

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "PreToolUse": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "PermissionRequest": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "Notification": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "SessionEnd": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }]
  }
}
```

把 `<you>` 换成你的用户名。

## 开发

Go 版本由 [mise](https://mise.jdx.dev) 管理（`.mise.toml`）。

```bash
go run .
```

```bash
go test ./...
```

```bash
SPARK_REAL=1 go test ./internal/monitor -run TestScanRealSessions -v
```

最后这条会打印本机扫描到的真实会话和状态。

## 打包成 .app

```bash
go tool mygo build -skip-dmg
```

产物在 `build/darwin-arm64/Spark.app`，bundle id 是 `dev.spark.app`，图标取自 `resources/icon.png`。窗口位置和大小记在 `~/Library/Application Support/Spark/window-state.json`。

没有设 `LSUIElement`，所以它是个正常的 Dock 应用；想改成纯菜单栏常驻，在 `mygo.json` 里加 `"macos": {"infoPlist": {"LSUIElement": true}}`。
