# 聚宽回测 Web 接口分析与扩展方案

采集日期：2026-09-07（Asia/Shanghai）。

结论：现有客户端可以扩展回测功能。登录会话、统一响应包和只读 GET 请求封装可复用；策略标识、回测状态、分页游标、统计指标和持仓行需要独立建模。不能把现有 `/algorithm/live/*` 方法直接换成 `/algorithm/backtest/*`。

回测 Go 查询、新建策略、创建回测、取消与完整导出方法现已实现，使用方式见 [BACKTEST_API.md](./BACKTEST_API.md)。2026-09-07 后续联调已真实执行新建策略、提交回测、结果归档与免费权益完整导出；取消仅在本地截获 POST 验证，未执行线上取消或积分扣费。最初浏览器采集时，编辑页加载后自身触发了一次 `/algorithm/index/save` 自动保存，没有编辑策略代码或回测参数。

## 1. 证据范围

- 实际访问：策略列表、单策略历史回测列表、已完成回测详情、策略编辑页。
- 实际读取：全账户回测列表、汇总统计、收益曲线、持仓、成交、日志、错误日志、性能分析、风险指标、回测源码、运行时间、交易日。
- 实际分页：收益曲线读取至空页；成交读取至空页；持仓读取至上游限制。
- 新建策略、创建回测、完整导出：前端和服务端结果均已验证。取消、克隆：从当前页面表单和前端调用代码确认，未验证线上执行结果。
- 最初样本的普通日志、错误日志、性能分析为空；实现阶段已补验非空普通日志，offset=0/1 各返回 100 条，响应 offset 为请求的起始游标。非空错误日志和性能分析仍待补充。

前端来源：

- [策略列表脚本](https://cdn.joinquant.com/std/algorithm/js/list.min.js?v=1784718693311)
- [回测列表脚本](https://cdn.joinquant.com/themes/jq/static/algorithm/js/backtestList.min.js?v=1784718693311)
- [回测详情脚本](https://cdn.joinquant.com/themes/jq/static/algorithm/js/detail.min.js?v=1784718693311)
- [策略编辑脚本](https://cdn.joinquant.com/themes/jq/static/algorithm/js/edit.min.js?v=1784718693311)

这些是网页内部接口，不是官方 JQData API。下文占位符均不包含真实账号标识、Cookie、页面 token 或策略源码。

## 2. 请求协议与认证

站点：`https://www.joinquant.com`。

JSON 接口继续使用现有统一响应包：

```json
{"data": {}, "status": "0", "code": "00000", "msg": ""}
```

`status` 是请求是否成功；`data.state` / `data.status` 才是任务或数据生成状态，三者不能混为一谈。

网页的 `Cy.ajax` 默认发 POST，参数经常放在 URL 查询串内，POST 表单额外带 `ajax` 和 `token`。不能只看 URL 就把浏览器请求记成 GET。

本次直接使用已登录 Cookie、`ajax=1`、`X-Requested-With: XMLHttpRequest`，不附加页面 token，验证以下接口也接受 GET，均返回 `code=00000`：

```text
/algorithm/backtest/stats
/algorithm/backtest/statsList
/algorithm/backtest/result
/algorithm/backtest/transactionInfo
/algorithm/backtest/positionInfo
/algorithm/backtest/log
/algorithm/backtest/error
/algorithm/backtest/profile
/algorithm/backtest/risk
/algorithm/backtest/source
/algorithm/backtest/runTimeInfo
/algorithm/backtest/items
```

因此首版查询方法可复用 `getEnvelope`。这是当前账号、普通自有策略上的实测结果，不代表写接口或受访问控制的策略也能省略 token。

Go 实际联调额外确认：`statsList` 需要同站 Referer。相同 Cookie/ID 在缺少 Referer 时返回 `code=20000`；单独更换为浏览器 User-Agent 无效，添加同站 Referer 后成功。`BacktestBatchStats` 已自动设置该请求头。`tradeDays` 也已补验 GET 成功。

编辑页和详情页在内联脚本中暴露 `window.tokenData = {name, value}`；网页表单请求使用它。实现写接口时应从新读取的编辑页提取，不能硬编码。

HTML 页面使用正常 GET，不加 `ajax=1`，也不发送 `X-Requested-With`，与现有 `getHTML` 约定一致。

## 3. 策略与回测列表

### 3.1 策略列表：HTML

```http
GET /algorithm/index/list
GET /algorithm/index/list?page={page}
GET /algorithm/index/list?fId={folderId}
GET /algorithm/index/list?query={name}
```

入口和分页链接已采集；文件夹及搜索参数来自页面链接、列表脚本，未逐项遍历验证。

表格为 `#algo_table`，行是 `.algorithm_list`：

| 元素 | 含义 |
| --- | --- |
| `td[_algorithmId]` | 策略操作使用的不透明 ID |
| `td[_fId]` | 文件夹 ID；此行不是策略 |
| 名称链接 `/algorithm/index/edit?algorithmId=...` | 打开编辑页的 ID |
| 历史回测链接 `/algorithm/backtest/list?algorithmId=...` | 打开该策略历史回测的 ID |
| 分类、修改日期、历史编译次数、历史回测次数 | 列表展示字段 |

同一行的操作 ID、编辑链接 ID、历史回测链接 ID 实际不同。应分别保留链接或对应字段。列表有文件夹和分页，仅解析当前页会遗漏策略。

页面的“展示行数”按钮调用 `/algorithm/index/setLimit` 修改页面偏好；它不是已验证的无副作用查询参数。客户端读取时优先跟随分页链接。

### 3.2 单策略历史回测：HTML

```http
GET /algorithm/backtest/list?algorithmId={historyAlgorithmId}
```

表格为 `#backtest-feed-table`，行是 `.backtest-tr`：

| 来源 | 建议字段 | 用途 |
| --- | --- | --- |
| `tr[_backtestId]` | `ListRunID` | 列表统计 `/statsList` |
| `tr[_backtestId2]` | `DetailID` | 打开 `/backtest/detail`；行点击代码实际使用此值 |
| `.source-code[_backtestId]` | `SourceID` | 读取该次回测源码 |
| `tr[_idx]` | `Index` | 列表序号 |
| `tr[_status]` | `State` | 回测状态 |

其余字段包含名称、创建时间、起止日期、初始资金、运行时长、频率、统计指标、状态文本、Python 版本。一部分列默认隐藏，但仍在 HTML 中。

**重复读取同一历史列表后，同一条回测的 `ListRunID` 和 `DetailID` 都发生变化。** 应视作页面生成的不透明临时值，不用作长期数据库主键，也不要根据名称、字符串相似性或数字外观推导其他 ID。

### 3.3 全账户历史回测：JSON

```http
GET /algorithm/backtest/items?page=1&limit=10&keyword={keyword}&ajax=1
```

```text
data.page
data.keyword
data.total                 实测为数字字符串
data.list[].backtestId
data.list[].name
data.list[].baseCapital     实测为数字字符串
data.list[].addTime
data.list[].startTime
data.list[].endTime
data.list[].frequency       实测为中文“每天”，不是提交用的 day
data.list[].status          实测为字符串
data.list[].statusText
data.list[].version         实测为字符串“3”
```

`page`、`limit` 已实际调用；`keyword` 来自前端搜索代码，非空搜索未单独验证。

**不要为此接口暴露“按 algorithmId 过滤”的承诺。** 实测添加一个仅有 1 条回测的策略 `algorithmId` 后，返回的总数仍是全账户总数，首条仍是其他策略。该参数在本次调用中未起过滤作用。

建议将全账户查询和单策略历史查询设计成两个方法。此 JSON 列表不返回策略 ID，不能仅凭它构造可靠的策略归属关系。

## 4. 详情页与 ID 解析

```http
GET /algorithm/backtest/detail?backtestId={DetailID}
```

进入详情页后，再从页面提取：

| 来源 | 用途 |
| --- | --- |
| `#backtestId.value` | 详情页的数据请求 ID，本文记为 `RunID` |
| `#code[_backtestId]` | 源码读取 ID，本文记为 `SourceID` |
| `#algorithmId.value` | 当前页面所用策略操作 ID |
| `#startDate` / `#endDate` | 回测起止日期 |
| `#frequency` 的 `value` 属性 | 运行频率 |
| 页面设置区域 | 初始资金、Python 版本等 |

页面还包含 `#backtest-decryptId`、`#local_algorithmId` 等内部标识。本次没有验证它们能否用于 API 请求或长期定位，不应直接代替上述 ID。

推荐读取流程：

```text
ListAlgorithms
  → 单策略历史列表
  → Backtest DetailID
  → 读取详情 HTML，解析 RunID / SourceID / 设置
  → 统计、曲线、成交、持仓、日志、源码
```

不同来源 ID 的可互换性没有全面验证，首版按页面实际用法传递。

## 5. 已验证的数据接口

下表 `backtest/*` 均位于 `/algorithm/backtest/`。JSON 查询统一附加 `ajax=1`。

| 功能 | 路径 | 参数 | 网页方法 / 本次补充验证 |
| --- | --- | --- | --- |
| 汇总统计 | `stats` | `backtestId=RunID` | POST / GET 成功 |
| 列表批量统计 | `statsList` | `algorithmId`、`backtestIds=ListRunID1,ListRunID2` | POST / 单 ID GET 成功；多 ID 未单独验证 |
| 收益曲线与状态 | `result` | `backtestId`、`offset`、`userRecordOffset` | POST / GET 成功 |
| 历史成交 | `transactionInfo` | `backtestId`、`offset`、`dateOffset` | POST / GET 成功 |
| 历史持仓与现金行 | `positionInfo` | `backtestId`、`offset`、`dateOffset` | POST / GET 成功 |
| 普通日志 | `log` | `backtestId`、`offset` | POST / GET 成功；样本为空 |
| 错误日志 | `error` | `backtestId`；编辑页还传 `offset` | POST / GET 成功；样本为空 |
| 性能分析 | `profile` | `backtestId` | POST / GET 成功；样本为空 |
| 风险指标 | `risk` | `backtestId` | POST / GET 成功 |
| 回测代码快照 | `source` | `backtestId=SourceID` | POST / GET 成功 |
| 运行时间 | `runTimeInfo` | `backtestId` | GET 成功 |
| 交易日 | `tradeDays` | `startDay`、`endDay` | 网页 POST / 补充 GET 成功 |

### 5.1 汇总统计

`stats` 的 `data` 直接是指标对象，不是现有模拟交易 `Stats.Metrics` 的时间序列结构。

```text
trading_days
algorithm_return, benchmark_return, excess_return
annual_algo_return, annual_bm_return
alpha, beta, sharpe, sortino, information
algorithm_volatility, benchmark_volatility
max_drawdown, max_drawdown_period
excess_return_sharpe, avg_excess_return
excess_return_max_drawdown, excess_return_max_drawdown_period
win_ratio, day_win_ratio, profit_loss_ratio
win_count, lose_count, avg_trade_return, avg_position_days, turnover_rate
report_done_seconds, report_done_cpu_seconds
```

数值格式混用：

- `algorithm_return`、`max_drawdown` 等是小数比率，例如 `0.12` 表示 `12%`。
- `excess_return_sharpe` 在样本中是数字字符串。
- `avg_excess_return`、`excess_return_max_drawdown` 在样本中是带 `%` 的字符串。
- 最大回撤区间是两个日期组成的数组。

`statsList` 返回 `data[]`，每项含 `backtestId`、`stat`、`usedSec`；`stat` 同样是直接指标对象。

实现阶段还观察到 `stat=[]`、`risk=false` 的占位响应。客户端分别保留空指标映射、返回 `ErrBacktestNotReady`，不把占位值转换为收益零或完整空数据。

### 5.2 收益曲线与状态

```text
data.state
data.result.offset
data.result.count
data.result.benchmark.time[] / value[]
data.result.overallReturn.time[] / value[]
data.result.gains.earn.time[] / value[]
data.result.gains.lose.time[] / value[]
data.result.orders.buy.time[] / value[]
data.result.orders.sell.time[] / value[]
data.userRecord             本次样本为 null；前端按名称→time/value 序列处理
```

**曲线中的收益值是百分数单位，与 `stats` 不同。** 样本最后一个 `overallReturn.value` 为 `800.56`，同一回测的 `stats.algorithm_return` 约为 `8.0056`。归一化时必须明确 `/100`，不能混用。

时间戳也有差别：曲线 `time` 是毫秒；`tradeDays` 返回的整数数组是秒。应保留时间戳原意，展示使用 Asia/Shanghai，不能把交易日零点强加给曲线时间。

样本曲线真实分页：

| 请求 offset | 返回 offset | count | state |
| --- | --- | --- | --- |
| 0 | 0 | 1000 | 2 |
| 1000 | 1000 | 863 | 2 |
| 1863 | 1863 | 0 | 2 |

下一游标为 `返回 offset + count`。**任务完成不等于结果已经读完**：第一页就是 `state=2`，但后面还有数据。至少应读到终态下的空页，并校验游标前进，避免重复页死循环。

`userRecordOffset` 是独立游标，不能直接用收益曲线 offset 代替。自定义记录本次为空，非空样本的游标推进、终止条件须另行验证。

前端状态映射如下；本次服务端样本实际观察到的是 `2`：

| 值 | 前端含义 |
| --- | --- |
| 0 | 等待开始/排队 |
| 1 | 运行中 |
| 2 | 完成 |
| 3 | 失败 |
| 4 | 已取消 |

状态字段兼容字符串和数字，并保留未知值。失败、取消后仍应允许读取已有曲线和错误日志。

### 5.3 成交：按偏移与日期共同翻页

```json
{"data":{"status":"2","transaction":[],"max":false},"status":"0","code":"00000","msg":""}
```

首次请求仅带 `backtestId`；后续：

```text
offset     = 已读取的原始行数
dateOffset = 上一页最后一行的 tradeDate；没有则用 date
```

字段与现有 `Transaction` 大量重合，额外观察到 `tradeDate`。不能过滤零数量行后再计算 offset，也不能只按订单时间去重。

样本实际返回 `99 + 100 + 99 + 24 + 0` 行，总计 322 行。**不足 100 条不表示结束。** 上游会按日期处理批次，使用终态空页和 `max` 判断，而不是固定页长。

前端还兼容 `total` 为带括号的格式化字符串；本次所选样本没有验证该分支。直接复用 `Transaction.Total float64` 前，应覆盖这种格式差异。

### 5.4 持仓：现金行与展示上限

```json
{"data":{"status":"2","position":[],"max":false},"status":"0","code":"00000","msg":""}
```

下一页使用原始累计行数 `offset`，以及最后一行的 `date` 作为 `dateOffset`。行包含日期、持仓字段以及 `totalValue`，响应没有模拟交易 `Portfolio` 那样的顶层 `cash`、`totalValue`。

现金也作为一行返回，例如以下结构示意（数值已替换）：

```json
{
  "date": "2026-01-05",
  "time": "15:15:00",
  "security": "",
  "stock": "<span class=\"label label-cash\">Cash</span>",
  "amount": "",
  "value": 1234.56,
  "gain": "",
  "margin": "",
  "dailyGains": "",
  "totalValue": 0
}
```

实际把普通持仓行和现金行交给现有 `Position` 解析，Go 返回：

```text
json: cannot unmarshal string into Go struct field Position.data.position.gain of type float64
```

必须单独定义回测持仓行：识别现金行；为可缺失数字保留空值；保留 `date`；避免把现金当证券；也不能把现金行的 `totalValue=0` 当作当日总资产。样本现金可以为负，不应强制截为零。

**实测累计读取 1000 行后，下一请求返回空数组且 `max=true`，此时只到回测中段。** 前面每一页都为 `max=false`。因此：

1. `max=false` 只说明当前响应没有报告展示上限，不代表已经得到完整历史。
2. 空数组且 `max=true` 表示截断，不能按“读取完成”返回。
3. `max=true` 后应返回已读取数据和 `ErrTruncated`，完整历史走平台导出流程。
4. 完整性判断必须保留跨页状态；不能套用模拟持仓的 `isLimit` 字段名。

前端对成交也提供同样的 1000 条展示上限提示；本次成交样本未达到该上限。

### 5.5 日志、源码、运行时间和风险指标

```text
log       → data.state, data.logArr[], data.offset, data.max
error     → data.state, data.logArr[]
profile   → data.state, data.profile
source    → data.source（字符串，回测当时的代码快照）
```

编辑页代码可能已修改，不能把它当作历史回测代码；历史审计使用 `/backtest/source`。

`runTimeInfo.data` 包含：

```text
startTime, endTime, addTime, runStartTime, cancelTime, currentTime
needSeconds, untilNowSec, status, creditsLog
```

`cancelTime` 样本为 `0000-00-00 00:00:00`，不能直接按有效 Go 时间解析。`creditsLog` 在样本中是空数组，前端又会读取其对象属性，应暂用 `json.RawMessage`，补充付费样本后再收紧类型。

`risk.data.risk` 包含以下数组：

```text
algorithmPeriodReturn, benchmarkPeriodReturn
alpha, beta, sharp, sortino, information
algovolatility, benchmarkvolatility, maxdrawdown
```

上游拼写是 `sharp`，不是 `sharpe`。每个数组元素包含 `date`（年月）、`1month`、`3month`、`6month`、`12month`；窗口不足时返回字符串 `N/A`。数值和缺失标记都必须保留，不可把 `N/A` 解析为 0。

## 6. 创建、保存、取消回测：创建已实测，保存/取消待验证

### 6.1 创建回测

```http
POST /algorithm/index/build
Content-Type: application/x-www-form-urlencoded
```

普通自有代码策略的页面表单与调用代码给出：

| 字段 | 来源或取值 |
| --- | --- |
| `algorithm[algorithmId]` | 当前编辑页隐藏字段；保存响应可能更新它 |
| `algorithm[userId]` | 当前编辑页隐藏字段 |
| `algorithm[accessControl]` | 当前编辑页；本次普通代码策略为 `0` |
| `algorithm[name]` | 策略名称 |
| `algorithm[code]` | Python 源码先转 UTF-8 字节，再标准 Base64 编码 |
| `encrType` | `base64`，只是传输编码 |
| `backtest[type]` | `0` = 完整回测，`1` = 编辑器内的编译运行 |
| `backtest[startTime]` | 例如 `2026-01-01 00:00:00` |
| `backtest[endTime]` | 例如 `2026-01-31 23:59:59` |
| `backtest[baseCapital]` | 初始资金 |
| `backtest[frequency]` | `day` / `minute` / `tick`；权限检查由平台决定 |
| `backtest[pyVersion]` | 当前页面默认 `3`；页面标注 Python2 已下线 |
| `ajax` | `1` |
| `token` | 当前页面 `window.tokenData.value` |
| `useCredit` | 可选；前端在用户同意积分扣费后添加 `1` |

表单还包含 `fontpref`、`themepref` 等编辑器偏好，不应作为业务层必填参数。上表是浏览器提交字段集合，尚未逐字段测试服务端最小必填集合。

前端成功处理逻辑读取 `data.algorithmId`、`data.backtestId`、`data.backtestId_`；编译运行还读取 `data.tradeDays`。**这些返回字段来自代码分析，尚无本次实际 build 成功响应。** 完整回测随后跳转到 `/algorithm/backtest/detail?backtestId=...`。

Go 编码对应：

```go
form.Set("algorithm[code]", base64.StdEncoding.EncodeToString([]byte(source)))
form.Set("encrType", "base64")
form.Set("backtest[type]", "0")
```

使用 `url.Values.Encode()` 编码整个表单，避免 Base64 中的 `+` 在表单解码时变成空格。

页面调用链从编辑表单直接进入 `/index/build`，没有强制先调用 `/index/save`。创建任务是否同时持久化策略代码与设置，仍需实际写入实验确认，不能假设它是无副作用的临时代码运行。

### 6.2 保存与取消

```http
POST /algorithm/index/save
POST /algorithm/index/cancel
```

- 保存：使用相同的编码表单；编辑页自身自动保存已观察到成功响应 `data.algorithmId`。成功后页面替换当前策略操作 ID。
- 取消：前端发送 `backtestId=RunID`；本次没有执行取消，成功响应数据及幂等性未验证。
- 新建策略：列表中存在 `/algorithm/index/new?...` 模板入口，属于创建操作，即使使用 GET 也不能当作查询。后续已验证 `POST /algorithm/index/new?restore=0&type=empty&baseCapital=100000`，响应同源 `/algorithm/index/edit?algorithmId=...` 重定向；`CreateAlgorithm` 返回其中的编辑页 ID，不跟随重定向或重放请求。
- 克隆策略：编辑页调用 `POST /algorithm/index/cloneOwnAlgorithm`，表单 `sourceAlgorithmId`，前端读取 `data.algorithmId`；本次未执行。

### 6.3 配额与错误

`/algorithm/index/statistics` 实际返回 `data.running`、`data.done`、`data.duration.used`、`data.duration.free`；可用于展示配额和运行状态，但不能据此保证下一次提交一定被接受。

编辑脚本对 **`msg`** 等于 `50000`（免费回测时间不足）和 `50001`（积分为负）做特殊处理。不要未经验证就把这两个值只当成 envelope 的 `code`。

创建属于非幂等操作：网络超时不能盲目自动重试，否则可能生成重复任务。`useCredit=1` 也不能由通用错误重试器自动追加。

## 7. 超出展示上限的导出流程：已真实验证

| 步骤 | 网页请求 | 参数 / 前端处理 |
| --- | --- | --- |
| 创建打包任务 | `GET /algorithm/backtest/addExportZip` | `backtestId`、`type=transaction/position/log`、`useCredit=0` |
| 查询打包状态 | `GET /algorithm/backtest/getExportStatus` | `task` |
| 下载压缩包 | `GET /algorithm/backtest/getExportZip` | `task`；浏览器下载响应 |

前端按约 2 秒间隔查询打包状态。实测打包中返回布尔 `false`，完成返回字符串 `1`；前端另定义字符串 `2` 为无数据。申请成功的 data 是任务字符串。原始收益 CSV 实测为 GB18030，含自定义指标列；ZIP 包中为 CSV 或日志文本。

详情页交易、持仓、日志三个导出按钮的 `backtestid` 各不相同，不能复用 RunID。`GET /community/post/userInfo` 的 `vipType` 实测为 `VIP`；`/credits/index/getUserCreditsInfo?ruleKey=export_position` 等只返回积分价格，没有免费剩余次数字段。申请固定 `useCredit=0`，`code=30000` 表示需要确认扣费，SDK 返回错误，不会同意付费。

后续实际导出取得 243 行收益概述、279 条成交、1614 条持仓和 903 行日志。一次轮询中断后凭原任务继续下载，没有重复申请。

创建打包任务会触发服务端工作，可能涉及积分，虽然 HTTP 方法是 GET，仍应按有副作用操作处理。查询持仓遇到截断时应明确返回 `ErrTruncated`，不在只读方法中自动创建导出任务。

另有前端引用的辅助下载：

```text
GET /algorithm/backtest/export?backtestId=...&type=result
GET /algorithm/backtest/downloadZip?backtest_id=...
```

收益导出已在后续联调中下载；源码压缩包 `downloadZip` 未验证。`dayResult` 虽存在于详情脚本的接口映射，本次未捕获到实际调用，不把它计入已验证接口。

## 8. Go 客户端设计与实现范围

### 8.1 保持现有模拟 API 兼容

`ListStrategies` 目前返回模拟交易策略，不能直接改为代码策略。新增命名清晰的回测方法，复用已有会话、错误类型和 HTTP 层。

接口分组如下。查询、CreateAlgorithm、BuildBacktest、CancelBacktest 和完整导出已实现；保存/克隆策略保留为后续扩展。最新签名以 [BACKTEST_API.md](./BACKTEST_API.md) 为准：

| 模块 | 建议方法 |
| --- | --- |
| 策略入口 | `ListAlgorithms`、`ListBacktests`（单策略 HTML）、`ListAllBacktests`（全账户 JSON） |
| 任务详情 | `Backtest`（解析详情 HTML）、`BacktestRuntime` |
| 结果读取 | `BacktestStats`、`BacktestBatchStats`、`BacktestResultPage`、`BacktestRisk` |
| 明细读取 | `BacktestPositionPage`、`BacktestTransactionPage` |
| 排错与复现 | `BacktestLogs`、`BacktestErrors`、`BacktestProfile`、`BacktestSource` |
| 任务写入 | `CreateAlgorithm`、`BuildBacktest`、`CancelBacktest`；后续再加策略保存/克隆 |
| 完整导出 | `CreateBacktestExport`、`BacktestExportStatus`、`DownloadBacktestExport` |

### 8.2 必须独立的数据类型

- `AlgorithmRef`：分别保留编辑链接、历史回测链接及页面操作 ID。
- `BacktestRef`：分别保留 `ListRunID`、`DetailID`、`SourceID`，详情解析后取得数据 `RunID`。
- `BacktestState`：兼容数字/字符串，识别已知终态，未知状态不当作成功。
- `BacktestStats`：直接指标、明确比例单位、保留额外字段。
- `BacktestResultPage`：任务状态、当前 offset、count、收益序列、自定义记录。
- `BacktestPositionRow`：日期、证券/现金类型、可空数值，必要时保留原始展示字段。
- `BacktestTransactionRow`：保留 `tradeDate` 和格式化数字差异。
- 明细页结果：原始行数、下一 offset/dateOffset、`max`，不能丢弃截断状态。

可空数字要区分合法 0、空字符串、`null`、`N/A` 与非法值，不用一个宽松的“解析失败返回 0”转换器掩盖协议错误。

### 8.3 HTTP 层的变化

1. 已验证 GET 的查询复用现有 `getEnvelope` 及会话过期后续期一次的机制。
2. 新增表单 POST 封装，明确操作是否允许重放；不把“所有 POST”或“所有 GET”简单等同于有/无副作用。
3. `BuildBacktest` 成功返回后再读取详情，不能凭已发送请求就判定任务建立成功。
4. 写请求超时或连接中断应暴露提交结果未知，交由调用方查询历史并核对，禁止自动重复创建。
5. 解析列表、详情 HTML 时校验预期元素；登录页或页面结构变化不能伪装成空列表。
6. 下载采用有边界的流式读取，按实际响应类型处理，不能套用 JSON envelope。

### 8.4 实施顺序与验收重点

先实现已验证的只读链路：列表 → 详情 ID → 统计/曲线 → 明细/源码/日志。随后用独立的短周期测试策略验证 build 的真实请求与响应、任务状态变化、取消和重试边界；最后验证导出格式。

实现阶段应覆盖本次发现的实际边界：

- 同一回测的不同 ID 来源，以及列表刷新后 ID 改变。
- 全账户 `/items` 不按 `algorithmId` 过滤。
- `state=2` 但曲线还有下一页；游标不前进或序列长度不匹配。
- 成交 99 行仍有下一页；分页游标使用原始行数和 `tradeDate`。
- 持仓现金行的空数字字段、负现金、不能误用 `totalValue=0`。
- 先返回多页 `max=false`，随后空页 `max=true` 的截断场景。
- 统计小数比率、曲线百分数、风险指标 `N/A` 的区别。
- 写请求响应丢失不能重复提交；积分提示不自动同意。

实现已覆盖上述查询和写请求边界，并提供只读命令行示例。后续相邻 `joinquant` 项目已实测：新建独立策略、提交 5 日测试、故意抛错测试、实际目录策略全年回测、源码哈希核对、完整性标记和免费权益完整导出。线上取消、积分扣费仍未执行验证。
