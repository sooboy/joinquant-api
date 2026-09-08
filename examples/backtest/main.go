// Command backtest demonstrates read-only use of the backtest client.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	joinquant "github.com/sooboy/joinquant-api"
	"golang.org/x/term"
)

func main() {
	user := flag.String("user", "", "聚宽登录账号")
	detailID := flag.String("detail", "", "当前回测详情 ID；留空则列出历史回测")
	page := flag.Int("page", 1, "历史回测页码")
	limit := flag.Int("limit", 10, "每页回测数量")
	flag.Parse()
	if *user == "" {
		fmt.Print("账号: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			log.Fatal(err)
		}
		*user = strings.TrimSpace(line)
	}
	fmt.Print("密码: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		log.Fatal(err)
	}
	client, err := joinquant.New()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = client.Login(ctx, *user, string(password))
	clear(password)
	if err != nil {
		log.Fatal(err)
	}
	if *detailID == "" {
		result, err := client.ListAllBacktests(ctx, joinquant.AllBacktestOptions{Page: *page, Limit: *limit})
		if err != nil {
			log.Fatal(err)
		}
		for _, entry := range result.Entries {
			fmt.Printf("%s | %s | %s 至 %s | detail=%s\n", entry.Name, entry.StatusText, entry.StartTime, entry.EndTime, entry.BacktestID)
		}
		fmt.Printf("共 %d 条，下一页 %d（0 表示无下一页）。ID 刷新后可能变化。\n", result.Total, result.NextPage)
		return
	}
	detail, err := client.Backtest(ctx, *detailID)
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := client.BacktestRuntime(ctx, detail.RunID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s | 状态 %s | 耗时 %s\n", detail.Name, runtime.State, runtime.UntilNow)
	stats, err := client.BacktestStats(ctx, detail.RunID)
	if err != nil && !errors.Is(err, joinquant.ErrBacktestNotReady) {
		log.Fatal(err)
	}
	if stats != nil {
		raw, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(raw))
	}
	positions, err := client.BacktestPositionPage(ctx, detail.RunID, joinquant.BacktestCursor{})
	if err != nil && !errors.Is(err, joinquant.ErrTruncated) {
		log.Fatal(err)
	}
	if positions != nil {
		fmt.Printf("持仓首页 %d 行，截断=%v，下一游标=%+v\n", len(positions.Positions), positions.Max, positions.NextCursor)
	}
	transactions, err := client.BacktestTransactionPage(ctx, detail.RunID, joinquant.BacktestCursor{})
	if err != nil && !errors.Is(err, joinquant.ErrTruncated) {
		log.Fatal(err)
	}
	if transactions != nil {
		fmt.Printf("成交首页 %d 行，截断=%v，下一游标=%+v\n", len(transactions.Transactions), transactions.Max, transactions.NextCursor)
	}
}
