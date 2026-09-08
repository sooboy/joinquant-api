# 回测 API 使用说明

回测模块已实现，复用现有 `Client`、Cookie 会话和认证配置。现有模拟交易方法保持兼容。

验证范围（2026-09-07 更新）：真实账号查询、新建独立策略、提交回测、成功/失败结果读取、免费权益完整导出均已通过。取消使用真实页面提取字段和 token，在本地截获 POST 验证；尚未执行线上取消或积分扣费。保存/克隆代码策略暂未封装。协议证据见 [BACKTEST_ENDPOINTS.md](./BACKTEST_ENDPOINTS.md)。

## 查询方法

以下方法的第一个参数均为 `context.Context`。

| 方法 | 后续参数 | 返回值 |
| --- | --- | --- |
| `ListAlgorithms` | `AlgorithmListOptions` | `*AlgorithmPage`，含策略、文件夹、下一页 |
| `ListBacktests` | 策略 `HistoryID`、`BacktestListOptions` | `*BacktestListPage`，单策略历史 |
| `ListAllBacktests` | `AllBacktestOptions` | `*BacktestItemsPage`，全账户历史 |
| `Backtest` | `DetailID` | `*BacktestDetail`，解析当前 RunID/SourceID |
| `BacktestStats` | `RunID` | `BacktestStats`，原始指标映射 |
| `BacktestBatchStats` | 策略 `HistoryID`、若干 `ListRunID` | `[]BacktestBatchStat` |
| `BacktestResultPage` | `RunID`、`BacktestResultOptions` | `*BacktestResultPage` |
| `BacktestPositionPage` | `RunID`、`BacktestCursor` | `*BacktestPositionPage` |
| `BacktestTransactionPage` | `RunID`、`BacktestCursor` | `*BacktestTransactionPage` |
| `BacktestLogs` / `BacktestErrors` | `RunID`、offset | `*BacktestLogPage` |
| `BacktestSource` | `SourceID` | 回测代码快照字符串 |
| `BacktestRuntime` | `RunID` | `*BacktestRuntime` |
| `BacktestProfile` | `RunID` | `*BacktestProfile` |
| `BacktestRisk` | `RunID` | `BacktestRisk`，按指标分组的月度窗口 |
| `BacktestTradeDays` | 开始和结束 `time.Time` | `[]int64`，Unix 秒 |
| `BacktestUsage` | 无 | `*BacktestUsage`，运行任务及使用时长 |

列表一次读取一页。`Page=0` 默认第一页，`Limit=0` 默认 10；`NextPage=0` 表示没有后续页。文件夹需使用 `AlgorithmListOptions.FolderID` 单独读取。单策略历史与全账户历史是不同接口，`ListAllBacktests` 不提供上游会忽略的策略 ID 过滤参数。

## 读取历史回测

认证仍使用已有的 `client.Authenticate(ctx)` 或 `client.Login(ctx, username, password)`。认证完成后：

```go
page, err := client.ListAllBacktests(ctx, joinquant.AllBacktestOptions{Limit: 10})
if err != nil {
    return err
}
for _, item := range page.Entries {
    detail, err := client.Backtest(ctx, item.BacktestID)
    if err != nil {
        return err
    }
    stats, err := client.BacktestStats(ctx, detail.RunID)
    if err != nil {
        return err
    }
    value, err := stats.Number("algorithm_return")
    if err != nil {
        return err
    }
    if value.Valid {
        fmt.Printf("%s: %.2f%%\n", item.Name, value.Value*100)
    }
}
```

ID 均应按不透明值处理：

- `Algorithm.EditID` 用于创建回测时读取编辑页。
- `Algorithm.HistoryID` 用于单策略回测列表。
- `BacktestListEntry.DetailID` 或 `BacktestItem.BacktestID` 交给 `Backtest`。
- `BacktestDetail.RunID` 用于数据查询；`SourceID` 用于历史代码。
- `BacktestListEntry.ListRunID` 用于批量统计。

不要长期缓存这些 ID，也不要互相替换。重新读取列表和详情即可取得新的值。

## 分页与完整性

### 持仓和成交

第一次使用 `BacktestCursor{}`，后续原样传入上一页的 `NextCursor`。游标已包含全部原始行的数量及日期，不能根据过滤后的股票数量自行计算。

```go
cursor := joinquant.BacktestCursor{}
for {
    page, err := client.BacktestPositionPage(ctx, detail.RunID, cursor)
    if page != nil {
        // 即使 err 为 ErrTruncated，page 也可能包含本次取得的部分数据。
        for _, row := range page.Positions {
            if row.IsCash() {
                // row.Value 是现金；现金行 totalValue=0 不能当作账户总资产。
                continue
            }
            fmt.Println(row.Date, row.Code(), row.Value)
        }
    }
    if err != nil {
        // errors.Is(err, joinquant.ErrTruncated) 表示平台展示上限。
        return err
    }
    if page.Done {
        break
    }
    if page.NextCursor == cursor {
        // 运行中的任务可能暂时没有新数据，稍后再从此游标查询。
        return joinquant.ErrBacktestNotReady
    }
    cursor = page.NextCursor
}
```

`Done` 只在任务进入终态、返回空页且没有截断时为真。短页不表示结束；`Max=true` 返回页面和 `ErrTruncated`，包括上游返回空数组的情况。库不会自动创建收费导出任务，也不会把已截断的历史当作完整数据。

### 曲线和日志

- 曲线使用 `BacktestResultOptions.Offset`，下一次传 `page.NextOffset`。即使 `State=BacktestCompleted`，仍需读到 `Done=true`。
- 自定义 `record()` 数据的 `UserRecordOffset` 独立传递，不能替换成曲线 offset。库保留各序列；非对齐、多记录序列的自动聚合尚未提供，调用方应依据策略的记录方式管理该游标。
- 日志使用 `page.NextOffset`。普通日志遵循网页的非空记录计数，错误日志按返回记录数计数。
- 库不执行定时轮询。运行中的空页与未知状态都不会标记完成；调用方决定再次查询时间并提供可取消的 context。

## 数值与状态

`OptionalNumber` 通过 `Valid` 区分“缺失”和真实的零，兼容数字字符串、合法千分位以及 `N/A`、空字符串、`null`。百分数字符串自动转为比率；非法文本、非有限数字不会默认为零。

`BacktestStats.Number("algorithm_return")` 返回小数比率；曲线 `OverallReturn.Value` 保持上游百分数单位。比如 `0.125` 的统计收益对应 `12.5` 的曲线收益值，展示时不要再次对曲线乘 100。

持仓保留现金行和负现金，数量仍保留原始展示字符串；`Code()` 对现金行返回空字符串，`Quantity()` 对现金行返回错误。成交 `Total` 保留格式化原值，`TotalAmount()` 解析普通金额或网页括号中的金额。

已知终态为完成、失败、取消。风险数据返回 `false` / `0` / `null`，或单条统计为空时，方法返回 `ErrBacktestNotReady`；批量统计中的空数组转为空指标映射，调用方逐项检查。运行时间的无效取消时间原样保留，`creditsLog` 保留原始 JSON。

## 创建与取消

`client.CreateAlgorithm(ctx)` 新建一个独立、私有的云端策略，返回编辑页 ID。它是写操作，使用网页模板生成器支持的 POST 路径，接受同源编辑页重定向而不自动跟随；网络结果不明时返回 `ErrSubmissionUnknown`，不能直接重试。

```go
editID, err := client.CreateAlgorithm(ctx)
if err != nil { return err }
// 将 editID 传给 BuildBacktest；Source 指定本地代码，Name 可指定唯一任务名称。
```

在已有普通代码策略上创建一次完整回测：

```go
zone := time.FixedZone("Asia/Shanghai", 8*60*60)
job, err := client.BuildBacktest(ctx, algorithm.EditID, joinquant.BuildBacktestOptions{
    StartTime:      time.Date(2026, 1, 5, 0, 0, 0, 0, zone),
    EndTime:        time.Date(2026, 1, 6, 23, 59, 59, 0, zone),
    InitialCapital: 100000,
    Frequency:     joinquant.BacktestDaily,
    Mode:          joinquant.FullBacktest,
    AllowCredit:   false,
})
if err != nil {
    // ErrSubmissionUnknown：可能已经创建，先查历史，不能直接重试。
    return err
}
detail, err := client.Backtest(ctx, job.BacktestID)
```

`Source=nil` 使用当前编辑页代码；传 `Source: &source` 指定新代码；非空 `Name` 在表单中设置策略名称。**创建请求携带整个策略表单，可能同时更新已有策略的代码和设置**，需要隔离时先用 `CreateAlgorithm` 建立独立策略。受访问控制策略不支持此提交方式。

代码按 UTF-8 → Base64 → URL 表单编码；客户端读取新的页面 token，使用 Python3，时间转换为 Asia/Shanghai。`CompileBacktest` 对应编辑器内编译运行。`AllowCredit=false` 时不会继承页面中的扣费偏好，也不会因为配额错误自动追加 `useCredit=1`。

取消使用详情 ID：

```go
err := client.CancelBacktest(ctx, job.BacktestID)
```

写入规则：

1. 请求发出后不做自动登录重放或网络重试；阻止 307/308 跟随重发。
2. 连接中断、服务端 5xx、无效成功响应等返回 `*SubmissionError`；可用 `errors.Is(err, ErrSubmissionUnknown)` 判断，`errors.As` / `errors.Is` 仍能访问底层原因。
3. 明确的会话过期、权限或配额拒绝原样返回，不自动同意积分扣费。
4. 如果已收到有效任务 ID，但保存会话失败，会同时返回创建结果与持久化错误，保留用于核对的任务 ID。
5. 自定义 `WithHTTPClient` 的 Transport 也必须遵守不重放写请求的约定。

## 运行示例与验证

完整导出使用独立接口，按当前账号的免费权益申请：

```go
tier, err := client.Membership(ctx) // FREE / VIP / SVIP；未知类型保留原值
task, err := client.CreateBacktestExport(ctx, detailID, joinquant.ExportPositions)
// 必须在此持久化 task，后续只轮询和下载，避免重复消耗次数。
status, err := client.BacktestExportStatus(ctx, task) // 0 打包中，1 可下载，2 无数据
err = client.DownloadBacktestExport(ctx, task, destinationWriter)
// 收益概述直接下载 CSV，不创建 ZIP 任务。
err = client.DownloadBacktestSummary(ctx, detailID, summaryWriter)
```

ZIP 类型为 `ExportTransactions`、`ExportPositions`、`ExportLogs`。三个按钮的 `backtestid` 各不相同，SDK 从新详情页读取对应 ID。申请固定使用 `useCredit=0`，额度不足返回 `ErrExportCreditsRequired`，没有付费选项。虽然上游用 GET 申请任务，SDK 禁止自动重试、自动登录重放和重定向重放；结果不明返回 `ErrSubmissionUnknown`。自定义 Transport 也必须遵守此约定。

打包期间实测响应 `data:false`，完成时为 `data:"1"`；SDK 统一映射成状态整数。下载验证文件类型，阻止跨站重定向，限制原始导出 128 MiB。原始 CSV 可能使用 GB18030，SDK 保留字节；相邻本地工具会额外转换 UTF-8。会员查询不提供全账号当日剩余导出次数；实际免费权益由每次申请的服务端响应校验。

```bash
# 密码交互输入，不在命令行传递。
go run ./examples/backtest -user YOUR_USERNAME

# 查询一条回测的统计和首页明细。
go run ./examples/backtest -user YOUR_USERNAME -detail CURRENT_DETAIL_ID

go test -race ./...
```

示例只执行查询。测试覆盖临时 ID 分工、分页结束条件、现金空字段、数值单位、展示截断、页面 token、UTF-8 编码、失败响应分类以及写请求不重放。
