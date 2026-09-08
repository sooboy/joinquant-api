# 聚宽模拟交易 Web 接口记录

回测接口另见 [BACKTEST_ENDPOINTS.md](./BACKTEST_ENDPOINTS.md)，其中区分了已实测查询与仅从前端确认的写操作。

采集日期：2026-09-01  
入口：`https://www.joinquant.com/algorithm/trade/list`

以下请求来自聚宽已登录模拟交易页面的真实网络行为。全部属于 Web 内部接口，不承诺向后兼容。

## 通用协议

JSON 接口主要使用统一响应包：

```json
{
  "data": {},
  "status": "0",
  "code": "00000",
  "msg": ""
}
```

注意事项：

- `status`、`code`、`offset`、`totalCount` 在不同接口中可能是字符串或数字。
- JSON 请求需要登录 Cookie，并带 `ajax=1`。
- 会话失效可能返回错误码，也可能直接重定向到 `/user/login/index`。
- 模拟列表本身是 HTML，不能带 `ajax=1`，否则返回的不是完整列表页。

## 1. 登录状态

```http
GET /user/index/isLogin?ajax=1
```

核心响应字段：

```text
data.isLogin
data.userId
data.userName
data.alias
data.vipType
data.level
```

密码登录：

```http
POST /user/login/doLoginByText
Content-Type: application/x-www-form-urlencoded

username=...&pwd=...
```

登录可能返回验证码相关错误码，不能把拿到 `Set-Cookie` 直接视作登录成功，必须再次检查 `isLogin`。

## 2. 模拟策略列表

```http
GET /algorithm/trade/list
GET /algorithm/trade/list?process=1  # 进行中
GET /algorithm/trade/list?process=0  # 已关闭
```

响应为 HTML，表格为 `#live_table`。每行关键数据：

```text
tr[data-backtestspaceid]   活跃模拟空间 ID
tr[_status]                状态码
td[0][_backtestid]         数据接口使用的 RunID
td[1] a[href]              详情链接及 DetailID
td[1]                      策略名称
td[2]                      频率
td[3]                      状态文本
td[4]                      开始日期
```

实测同一策略在重新加载列表后，`RunID` 和详情链接中的 `DetailID` 会变化，因此必须按不透明临时标识处理。

## 3. 账户与持仓快照

```http
GET /algorithm/live/position
    ?limit=50
    &backtestId={RunID}
    &date=2026-09-01
    &isForward=1
    &field=
    &order=
    &ajax=1
```

该接口同时承担“账户详情”和“持仓详情”职责：

```text
data.cash                    聚宽可用资金
data.totalValue              聚宽总资产
data.isLimit                 持仓是否被截断
data.position[]              持仓列表
```

持仓字段：

```text
security                     品种
stock                        名称和代码，例如 名称(000001.XSHE)
side                         多空方向
amount / trueAmount          总持仓数量
closeableAmount              可平/可卖数量
price / truePrice            当前价格
value                        当前市值
gain                         持仓盈亏
avgCost / trueAvgCost        开仓均价
holdCost                     期货持仓均价
margin                       保证金
dailyGains                   当日盈亏
todayAmount                  今日数量变化
gainPercent                  盈亏比例
dailyGainsPercent            当日盈亏比例
todayGainsPercent            今日盈亏比例
positionPersent              页面格式化仓位比例（上游拼写如此）
```

等比例跟单使用：

```text
精确权重 = position.value / data.totalValue
现金权重 = data.cash / data.totalValue
```

不要优先使用格式化后的 `positionPersent`，它已经损失精度。

`isForward=1` 是详情页首次获取最近持仓时使用的参数；查询指定历史日期时可使用 `0`。

## 4. 下单与成交明细

```http
GET /algorithm/live/transactionDetail
    ?limit=200
    &backtestId={RunID}
    &date=2026-09-01
    &field=
    &order=
    &ajax=1
```

核心响应：

```text
data.transaction[]
data.isLimit
```

成交字段包括：

```text
date, time, matchTime
security, stock
transaction                  买/卖
type                         订单类型
amount, trueAmount           成交数量
orderAmount, trueOrderAmount 委托数量
price, truePrice             成交价格
limitPrice, trueLimitPrice   委托价格
total, gains, commission
status
transactionPersent           成交比例（上游拼写如此）
```

`trueAmount` 在卖出时可能带负号，且部分展示字段可能含 HTML。库同时提供保留方向和取绝对值的解析方法。

## 5. 单策略统计

```http
GET /algorithm/live/stat
    ?offset=-1
    &limit=1
    &backtestId={RunID}
    &ajax=1
```

`data.stat` 是指标名到时间序列的映射：

```json
{
  "max_drawdown": {
    "time": [1788192000000],
    "value": [0.12]
  }
}
```

已观察指标包括：

```text
alpha, beta, sharpe, sortino, information, volatility
benchmark_volatility, max_drawdown, algorithm_return
benchmark_return, annual_algo_return, trading_days
intraday_return, avg_excess_return
excess_return_max_drawdown, excess_return_sharpe
max_drawdown_period, monthly_return
```

部分指标值可能是数字字符串，部分指标如 `max_drawdown_period` 是嵌套数组。

## 6. 批量策略统计

```http
GET /algorithm/live/stats
    ?offset=-1
    &limit=1
    &backtestIds={RunID1},{RunID2}
    &ajax=1
```

返回 `data[]`，每项包含 `backtestId` 和 `stat`。

## 7. 当前收益

```http
GET /algorithm/backtest/currentResult
    ?backtestId={RunID1},{RunID2}
    &ajax=1
```

模拟列表使用该接口填充累计收益。返回 `data[]`，每项包含 `backtestId` 和 `result`。

## 8. 代码变更历史

```http
GET /algorithm/live/getLiveHistoryList
    ?backtestId={RunID}
    &page=1
    &limit=20
    &ajax=1
```

核心响应：

```text
data.list[].liveHistoryId
data.list[].backtestId
data.list[].sourceBacktestId
data.list[].addTime
data.list[].modTime
data.list[].status
data.list[].introduce
data.list[].showType
data.list[].getCodeType
data.totalCount
```

## 9. 页面使用但当前库未封装的辅助接口

```http
GET /algorithm/backtest/result
GET /algorithm/live/stockRiseFallRate
GET /algorithm/live/log
GET /algorithm/backtest/dayResult
```

这些接口用于收益曲线、当日涨跌幅和日志展示，不参与账户资金、仓位权重及成交跟踪，因此首版库不把它们作为稳定公共 API。

## 跟单一致性要求

完整目标仓位同步必须同时满足：

1. 同一次轮询中的 `cash`、`totalValue`、`position[]` 来自同一个 `/position` 响应。
2. `isLimit=false` 后才能将缺失标的解释为目标仓位零。
3. 使用本地策略独立账本计算下单差额，不能直接对比券商账户总持仓。
4. 下单后按真实成交更新本地策略账本。
5. 聚宽接口失败、字段解析失败或会话过期时停止本轮交易，禁止退回聚宽原始股数下单。
