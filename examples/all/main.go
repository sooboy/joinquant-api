package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	joinquant "github.com/sooboy/joinquant-api"
	"golang.org/x/term"
)

type testSummary struct {
	passed int
	failed int
}

func (s *testSummary) pass(format string, values ...any) {
	s.passed++
	fmt.Printf("[PASS] "+format+"\n", values...)
}

func (s *testSummary) fail(format string, values ...any) {
	s.failed++
	fmt.Printf("[FAIL] "+format+"\n", values...)
}

func main() {
	dateText := flag.String("date", time.Now().Format("2006-01-02"), "持仓和成交查询日期，格式 YYYY-MM-DD")
	maxStrategies := flag.Int("max-strategies", 0, "最多测试多少个策略，0 表示全部")
	showPositions := flag.Bool("show-positions", false, "输出持仓代码、数量和权重")
	showTransactions := flag.Bool("show-transactions", false, "输出成交代码、方向、数量和价格")
	timeout := flag.Duration("timeout", 30*time.Second, "每个接口的超时时间")
	sessionFile := flag.String("session-file", defaultSessionPath(), "Cookie 会话缓存文件（明文、权限 0600）")
	noSessionCache := flag.Bool("no-session-cache", false, "禁用 Cookie 会话缓存")
	flag.Parse()

	queryDate, err := time.ParseInLocation("2006-01-02", *dateText, time.Local)
	if err != nil {
		log.Fatalf("日期格式错误: %v", err)
	}

	reader := bufio.NewReader(os.Stdin)
	phone, err := promptLine(reader, "聚宽手机号: ")
	if err != nil {
		log.Fatalf("读取手机号失败: %v", err)
	}
	if phone == "" {
		log.Fatal("手机号不能为空")
	}

	var password string
	provider := joinquant.CredentialProviderFunc(func(context.Context) (string, string, error) {
		if password == "" {
			value, promptErr := promptPassword(reader, "聚宽密码: ")
			if promptErr != nil {
				return "", "", promptErr
			}
			password = value
		}
		return phone, password, nil
	})
	options := []joinquant.Option{joinquant.WithCredentialProvider(provider)}
	if !*noSessionCache {
		options = append(options, joinquant.WithSessionStore(phone, &fileSessionStore{path: *sessionFile}))
	}
	client, err := joinquant.New(options...)
	if err != nil {
		log.Fatal(err)
	}
	summary := &testSummary{}

	if err := withTimeout(*timeout, func(ctx context.Context) error {
		_, loginErr := client.Authenticate(ctx)
		return loginErr
	}); err != nil {
		printLoginError(err)
		os.Exit(1)
	}
	summary.pass("Authenticate: 会话可用")

	if err := withTimeout(*timeout, func(ctx context.Context) error {
		session, sessionErr := client.Session(ctx)
		if sessionErr == nil {
			summary.pass("Session: 用户=%s", nonEmpty(session.Alias, session.UserName))
		}
		return sessionErr
	}); err != nil {
		summary.fail("Session: %v", err)
	}

	var strategies []joinquant.Strategy
	if err := withTimeout(*timeout, func(ctx context.Context) error {
		var listErr error
		strategies, listErr = client.ListStrategies(ctx, joinquant.AllStrategies)
		return listErr
	}); err != nil {
		summary.fail("ListStrategies: %v", err)
		finish(summary)
	}
	if *maxStrategies > 0 && len(strategies) > *maxStrategies {
		strategies = strategies[:*maxStrategies]
	}
	summary.pass("ListStrategies: 获取 %d 个策略", len(strategies))

	runIDs := make([]string, 0, len(strategies))
	for _, strategy := range strategies {
		if strategy.RunID != "" {
			runIDs = append(runIDs, strategy.RunID)
		}
	}
	testBatchInterfaces(client, runIDs, *timeout, summary)

	for index, strategy := range strategies {
		fmt.Printf("\n--- 策略 %d/%d: %s [%s] ---\n", index+1, len(strategies), strategy.Name, strategy.Status)
		testStrategy(client, strategy, queryDate, *timeout, *showPositions, *showTransactions, summary)
	}
	testFindStrategy(client, strategies, *timeout, summary)

	finish(summary)
}

func testFindStrategy(client *joinquant.Client, strategies []joinquant.Strategy, timeout time.Duration, summary *testSummary) {
	for _, strategy := range strategies {
		if strategy.SpaceID == "" {
			continue
		}
		err := withTimeout(timeout, func(ctx context.Context) error {
			found, findErr := client.FindStrategy(ctx, strategy.SpaceID)
			if findErr == nil && found.SpaceID != strategy.SpaceID {
				return fmt.Errorf("返回了错误的 SpaceID: %s", found.SpaceID)
			}
			return findErr
		})
		if err != nil {
			summary.fail("FindStrategy: %v", err)
		} else {
			summary.pass("FindStrategy: SpaceID=%s", strategy.SpaceID)
		}
		return
	}
	fmt.Println("[SKIP] FindStrategy: 当前列表没有带 SpaceID 的活跃策略")
}

func testBatchInterfaces(client *joinquant.Client, runIDs []string, timeout time.Duration, summary *testSummary) {
	if len(runIDs) == 0 {
		fmt.Println("[SKIP] BatchStats/CurrentResults: 没有可用 RunID")
		return
	}

	err := withTimeout(timeout, func(ctx context.Context) error {
		stats, statsErr := client.BatchStats(ctx, runIDs...)
		if statsErr == nil {
			summary.pass("BatchStats: 返回 %d 项", len(stats))
		}
		return statsErr
	})
	if err != nil {
		summary.fail("BatchStats: %v", err)
	}

	err = withTimeout(timeout, func(ctx context.Context) error {
		results, resultErr := client.CurrentResults(ctx, runIDs...)
		if resultErr == nil {
			summary.pass("CurrentResults: 返回 %d 项", len(results))
		}
		return resultErr
	})
	if err != nil {
		summary.fail("CurrentResults: %v", err)
	}
}

func testStrategy(
	client *joinquant.Client,
	strategy joinquant.Strategy,
	date time.Time,
	timeout time.Duration,
	showPositions bool,
	showTransactions bool,
	summary *testSummary,
) {
	err := withTimeout(timeout, func(ctx context.Context) error {
		stats, statsErr := client.Stats(ctx, strategy.RunID)
		if statsErr == nil {
			names := make([]string, 0, len(stats.Metrics))
			for name := range stats.Metrics {
				names = append(names, name)
			}
			sort.Strings(names)
			summary.pass("Stats: %d 个指标", len(names))
		}
		return statsErr
	})
	if err != nil {
		summary.fail("Stats: %v", err)
	}

	err = withTimeout(timeout, func(ctx context.Context) error {
		portfolio, portfolioErr := client.Portfolio(ctx, strategy.RunID, date)
		if portfolioErr != nil {
			return portfolioErr
		}
		if portfolio.IsLimit {
			return fmt.Errorf("%w: portfolio", joinquant.ErrTruncated)
		}
		summary.pass("Portfolio: 总资产=%.2f 可用资金=%.2f 持仓=%d", portfolio.TotalValue, portfolio.Cash, len(portfolio.Positions))
		if showPositions {
			for _, item := range portfolio.Positions {
				quantity, quantityErr := item.Quantity()
				closeable, closeableErr := item.CloseableQuantity()
				if quantityErr != nil || closeableErr != nil {
					fmt.Printf("       %s 数量解析失败: %v %v\n", item.Code(), quantityErr, closeableErr)
					continue
				}
				weight := 0.0
				if portfolio.TotalValue > 0 {
					weight = item.Value / portfolio.TotalValue
				}
				fmt.Printf("       %s %-12s 数量=%g 可卖=%g 权重=%.4f%%\n",
					item.Code(), item.Name(), quantity, closeable, weight*100)
			}
		}
		return nil
	})
	if err != nil {
		summary.fail("Portfolio: %v", err)
	}

	err = withTimeout(timeout, func(ctx context.Context) error {
		page, transactionErr := client.Transactions(ctx, strategy.RunID, date)
		if transactionErr != nil {
			return transactionErr
		}
		if page.IsLimit {
			return fmt.Errorf("%w: transactions", joinquant.ErrTruncated)
		}
		summary.pass("Transactions: %d 条", len(page.Transactions))
		if showTransactions {
			for _, item := range page.Transactions {
				quantity, quantityErr := item.FilledQuantity()
				if quantityErr != nil {
					fmt.Printf("       %s %s 数量解析失败: %v\n", item.Code(), item.Transaction, quantityErr)
					continue
				}
				fmt.Printf("       %s %-2s 数量=%g 价格=%.3f 状态=%s\n",
					item.Code(), item.Transaction, quantity, item.TruePrice, item.Status)
			}
		}
		return nil
	})
	if err != nil {
		summary.fail("Transactions: %v", err)
	}

	err = withTimeout(timeout, func(ctx context.Context) error {
		history, historyErr := client.LiveHistory(ctx, strategy.RunID, 1, 20)
		if historyErr == nil {
			summary.pass("LiveHistory: 返回 %d 条，总数=%s", len(history.Entries), history.TotalCount)
		}
		return historyErr
	})
	if err != nil {
		summary.fail("LiveHistory: %v", err)
	}
}

func promptLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	value, err := reader.ReadString('\n')
	if err != nil && len(value) == 0 {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func promptPassword(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	stdinFD := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFD) {
		value, err := term.ReadPassword(stdinFD)
		fmt.Println()
		return string(value), err
	}
	value, err := reader.ReadString('\n')
	return strings.TrimRight(value, "\r\n"), err
}

func withTimeout(timeout time.Duration, action func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return action(ctx)
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "<unknown>"
}

func printLoginError(err error) {
	var apiError *joinquant.APIError
	switch {
	case errors.Is(err, joinquant.ErrSessionExpired):
		log.Printf("登录失败或会话未建立: %v", err)
	case errors.As(err, &apiError):
		log.Printf("聚宽登录失败: code=%s status=%s message=%s", apiError.Code, apiError.Status, apiError.Message)
		if apiError.Code == "105" {
			log.Printf("该账号触发图片验证码，请先在浏览器完成一次登录后再测试")
		}
	default:
		log.Printf("登录请求失败: %v", err)
	}
}

func finish(summary *testSummary) {
	fmt.Printf("\n=== 测试结束: PASS=%d FAIL=%d ===\n", summary.passed, summary.failed)
	if summary.failed > 0 {
		os.Exit(1)
	}
}
