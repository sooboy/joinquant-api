# joinquant-api

聚宽模拟交易和回测网页接口的 Go 客户端。

本库直接覆盖聚宽模拟交易页面实际使用的接口：

- 模拟策略列表
- 登录状态
- 账户现金和总资产
- 当前及历史持仓
- 下单/成交明细
- 策略统计指标
- 代码变更历史
- 批量当前收益
- 回测策略、全账户及单策略历史列表
- 回测统计、收益曲线、历史持仓和成交、日志、风险指标及代码快照
- 新建独立代码策略，创建完整回测或编译运行、取消回测（写请求不自动重放）

> 这些是聚宽 Web 页面内部接口，不是官方 JQData API。库已经集中处理登录失效、JSON 协议错误、数字/字符串混用和数据截断，但上游网页改版时仍可能需要同步更新。

## 安装

```bash
go get github.com/sooboy/joinquant-api
```

## 持久会话并获取仓位

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	joinquant "github.com/sooboy/joinquant-api"
)

func main() {
	ctx := context.Background()
	client, err := joinquant.New(
		joinquant.WithSessionStore("account-id", sessionStore),
		joinquant.WithCredentialProvider(credentialProvider),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 先恢复 Cookie 并验证；只有会话失效时才向 provider 读取密码登录。
	if _, err := client.Authenticate(ctx); err != nil {
		log.Fatal(err)
	}

	strategies, err := client.ListStrategies(ctx, joinquant.RunningStrategies)
	if err != nil {
		log.Fatal(err)
	}
	for _, strategy := range strategies {
		portfolio, err := client.Portfolio(ctx, strategy.RunID, time.Now())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: 总资产=%.2f 可用资金=%.2f\n",
			strategy.Name, portfolio.TotalValue, portfolio.Cash)
	}
}
```

`SessionStore` 和 `CredentialProvider` 是抽象接口：库不依赖文件、数据库、
Keychain 或业务配置。`SessionStore` 保存的是 bearer Cookie，具体实现必须对其
加密并限制访问权限。所有只读接口遇到会话过期时只自动续期一次；并发请求只会
触发一次登录。

如果账号触发图片验证码，`Login` 会返回包含聚宽错误码的 `*APIError`。此时应由上层完成交互式登录，再通过 `WithCookies` 注入会话 Cookie；不要把 Cookie 写入日志或代码仓库。

`Authenticate` 和 `Login` 优先复用该 Client 账户的有效会话。普通网络或接口读取错误不会触发密码登录；失败登录默认冷却 5 分钟，明确的账号/验证码等拒绝进入 `ErrLoginBlocked`，需要用户处理。只读请求续登仍最多重试一次，回测创建、提交、导出等写请求不会自动重放。

多 Client / 多进程共享一个账户时，同时配置 `WithSessionStore(accountID, store)` 和 `WithLoginGuard(guard)`。`LoginGuard` 提供按账户的跨进程 `Lock` 及 `LoginState` 的原子 `Load` / `Save`；SDK 拿锁后重新读取、验证缓存，其他进程已续期就不再登录。密码请求前持久化冷却标记，崩溃重启也不能立即重发。`LoginState` 不含密码或远端消息文本。上层需复用每个账户的 Client，保护会话和凭据文件。

`ResetLoginProtection(ctx)` 仅用于用户明确修正凭据或完成验证后的操作，不得放进自动重试循环。未配置持久化 Guard 时，保护只在当前 Client 内生效。

## 分层边界

本库是纯接口层，只返回聚宽策略、仓位和成交数据，不包含资金分配、目标股数、
轮询、下单和策略账本。业务层应基于完整仓位自行计算：

```text
聚宽标的权重 = 聚宽标的市值 / 聚宽总资产
本地目标市值 = 分配给该策略的本地资金 × 聚宽标的权重
本地目标数量 = 向下取整(本地目标市值 / 本地执行价 / 每手数量) × 每手数量
本地下单差额 = 本地目标数量 - 本地策略账本持仓
```

关键约束：

1. `portfolio.Cash / portfolio.TotalValue` 自然保留聚宽现金仓位，不要再对本地可用资金套“1/10仓”等档位。
2. 本地当前数量必须来自“策略 × 本地账户”的独立持仓账本，不能使用证券账户的全部持仓。
3. 本地账本中存在、但完整聚宽快照中不存在的股票，其目标权重为零。
4. `portfolio.IsLimit == true` 表示结果被截断，不能执行差额调仓。
5. 手数、舍入、资金和可卖数量属于券商及业务规则，不由本库推断。

## 标识符说明

模拟列表同时暴露三种标识：

| 字段 | 来源 | 用途 |
|---|---|---|
| `Strategy.SpaceID` | `data-backtestspaceid` | 活跃模拟空间标识；用于重新查找策略 |
| `Strategy.RunID` | 首列 `_backtestid` | 调用持仓、成交、统计接口 |
| `Strategy.DetailID` | 详情链接的 `backtestId` | 打开网页详情 |

`RunID` 和 `DetailID` 是网页生成的临时不透明值，刷新模拟列表后可能变化。不要持久化后长期复用；轮询时应定期重新调用 `ListStrategies`，以 `SpaceID` 重新解析当前 `RunID`。

已停止的历史策略可能没有 `SpaceID`，只能作为一次性查询结果使用。

## 错误处理

```go
switch {
case errors.Is(err, joinquant.ErrSessionExpired):
	// 重新登录
case errors.Is(err, joinquant.ErrTruncated):
	// 提高 limit 或停止本轮调仓
default:
	var apiErr *joinquant.APIError
	if errors.As(err, &apiErr) {
		// 记录错误码，避免记录 Cookie 和账号信息
	}
}
```

完整接口记录见 [ENDPOINTS.md](./ENDPOINTS.md)。

回测方法、分页示例和创建参数见 [BACKTEST_API.md](./BACKTEST_API.md)，协议采集记录见 [BACKTEST_ENDPOINTS.md](./BACKTEST_ENDPOINTS.md)。查询、新建策略、提交回测均已真实联调；取消已验证真实页面表单和本地截获测试，尚未执行线上取消。相邻 `../joinquant` 项目提供本地策略提交、自动归档和结果面板。

回测查询示例：`go run ./examples/backtest -user YOUR_USERNAME`。历史持仓/成交达到平台展示上限时返回部分页面和 `ErrTruncated`；可显式使用 `CreateBacktestExport` / `BacktestExportStatus` / `DownloadBacktestExport` 按免费权益导出全部记录。不会自动同意积分扣费。

## 交互式全接口测试

仓库提供了一个可直接运行的测试程序，密码输入不会回显：

```bash
go run ./examples/all
```

默认查询当天并测试全部策略。常用参数：

```bash
# 指定查询日期，并输出持仓和成交详情
go run ./examples/all \
  -date 2026-09-01 \
  -show-positions \
  -show-transactions

# 只测试前两个策略
go run ./examples/all -max-strategies 2
```

程序依次测试：`Login`、`Session`、`ListStrategies`、`FindStrategy`、`BatchStats`、`CurrentResults`，以及每个策略的 `Stats`、`Portfolio`、`Transactions` 和 `LiveHistory`。任一接口失败时最终进程返回非零状态。
