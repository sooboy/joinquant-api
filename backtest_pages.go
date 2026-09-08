package joinquant

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type Algorithm struct {
	OperationID   string
	EditID        string
	HistoryID     string
	Name          string
	Kind          string
	ModifiedAt    string
	BuildCount    int
	BacktestCount int
	EditURL       string
	HistoryURL    string
}

type AlgorithmFolder struct{ ID, Name, URL string }

// AlgorithmSource reads the current editor code, including strategies that have
// not yet produced a backtest. It does not save or execute the strategy.
func (c *Client) AlgorithmSource(ctx context.Context, editID string) (string, error) {
	if strings.TrimSpace(editID) == "" {
		return "", fmt.Errorf("editor ID is required")
	}
	doc, err := c.backtestHTML(ctx, "/algorithm/index/edit", url.Values{"algorithmId": {editID}})
	if err != nil {
		return "", err
	}
	code := elementByID(doc, "code")
	if code == nil || elementByID(doc, "algorithmId") == nil {
		return "", backtestProtocol("editor source missing")
	}
	return rawNodeText(code), nil
}

type AlgorithmListOptions struct {
	Page     int
	FolderID string
	Query    string
}

// AlgorithmPage is one HTML page. Follow NextPage and recurse through Folders
// explicitly to enumerate the whole account. IDs are opaque and may change.
type AlgorithmPage struct {
	Algorithms []Algorithm
	Folders    []AlgorithmFolder
	Page       int
	NextPage   int
}

func (c *Client) ListAlgorithms(ctx context.Context, options AlgorithmListOptions) (*AlgorithmPage, error) {
	page, err := backtestPageNumber(options.Page)
	if err != nil {
		return nil, err
	}
	q := url.Values{"page": {strconv.Itoa(page)}}
	if options.FolderID != "" {
		q.Set("fId", options.FolderID)
	}
	if options.Query != "" {
		q.Set("query", options.Query)
	}
	doc, err := c.backtestHTML(ctx, "/algorithm/index/list", q)
	if err != nil {
		return nil, err
	}
	table := elementByID(doc, "algo_table")
	if table == nil {
		return nil, backtestProtocol("algorithm table missing")
	}
	out := &AlgorithmPage{Page: page, Algorithms: []Algorithm{}, Folders: []AlgorithmFolder{}}
	for _, row := range findElements(table, func(n *html.Node) bool { return n.Data == "tr" && hasNodeClass(n, "algorithm_list") }) {
		cells := directElementChildren(row, "td")
		if len(cells) < 6 {
			return nil, backtestProtocol("invalid algorithm row")
		}
		link := findElement(cells[1], func(n *html.Node) bool { return n.Data == "a" })
		if link == nil {
			return nil, backtestProtocol("algorithm link missing")
		}
		href := nodeAttribute(link, "href")
		if folderID := nodeAttribute(cells[0], "_fId"); folderID != "" {
			out.Folders = append(out.Folders, AlgorithmFolder{ID: folderID, Name: normalizedText(link), URL: href})
			continue
		}
		a := Algorithm{OperationID: nodeAttribute(cells[0], "_algorithmId"), Name: normalizedText(link),
			EditURL: href, EditID: queryID(href, "algorithmId"), Kind: normalizedText(cells[2]), ModifiedAt: normalizedText(cells[3])}
		if a.OperationID == "" || a.EditID == "" || a.Name == "" {
			return nil, backtestProtocol("algorithm identifiers missing")
		}
		a.BuildCount, err = strconv.Atoi(normalizedText(cells[4]))
		if err != nil {
			return nil, backtestProtocol("invalid build count")
		}
		a.BacktestCount, err = strconv.Atoi(normalizedText(cells[5]))
		if err != nil {
			return nil, backtestProtocol("invalid backtest count")
		}
		if history := findElement(cells[5], func(n *html.Node) bool { return n.Data == "a" }); history != nil {
			a.HistoryURL = nodeAttribute(history, "href")
			a.HistoryID = queryID(a.HistoryURL, "algorithmId")
		}
		out.Algorithms = append(out.Algorithms, a)
	}
	out.NextPage, err = nextHTMLPage(doc, page)
	return out, err
}

type BacktestListOptions struct {
	Page  int
	Query string
}

type BacktestListEntry struct {
	ListRunID      string
	DetailID       string
	SourceID       string
	Index          string
	Name           string
	State          BacktestState
	CreatedAt      string
	StartDate      string
	EndDate        string
	InitialCapital OptionalNumber
	Frequency      string
	UsedTime       string
	PythonVersion  string
}

type BacktestListPage struct {
	Entries  []BacktestListEntry
	Page     int
	NextPage int
}

// ListBacktests reads one strategy's HTML history. Pass Algorithm.HistoryID,
// not its EditID. ListAllBacktests uses a separate, account-wide JSON endpoint.
func (c *Client) ListBacktests(ctx context.Context, historyAlgorithmID string, options BacktestListOptions) (*BacktestListPage, error) {
	if strings.TrimSpace(historyAlgorithmID) == "" {
		return nil, fmt.Errorf("history algorithm ID is required")
	}
	page, err := backtestPageNumber(options.Page)
	if err != nil {
		return nil, err
	}
	q := url.Values{"algorithmId": {historyAlgorithmID}, "page": {strconv.Itoa(page)}}
	if options.Query != "" {
		q.Set("query", options.Query)
	}
	doc, err := c.backtestHTML(ctx, "/algorithm/backtest/list", q)
	if err != nil {
		return nil, err
	}
	table := elementByID(doc, "backtest-feed-table")
	if table == nil {
		return nil, backtestProtocol("history table missing")
	}
	out := &BacktestListPage{Page: page, Entries: []BacktestListEntry{}}
	for _, row := range findElements(table, func(n *html.Node) bool { return n.Data == "tr" && hasNodeClass(n, "backtest-tr") }) {
		e := BacktestListEntry{ListRunID: nodeAttribute(row, "_backtestId"), DetailID: nodeAttribute(row, "_backtestId2"),
			Index: nodeAttribute(row, "_idx"), State: BacktestState(nodeAttribute(row, "_status"))}
		name := elementByClass(row, "backtest-name")
		if name != nil {
			e.Name = normalizedText(name)
		}
		if source := elementByClass(row, "source-code"); source != nil {
			e.SourceID = nodeAttribute(source, "_backtestId")
		}
		cells := directElementChildren(row, "td")
		if len(cells) < 8 || e.ListRunID == "" || e.DetailID == "" || e.Name == "" || e.State == "" {
			return nil, backtestProtocol("invalid history row")
		}
		dates := backtestDatePattern.FindAllString(normalizedText(cells[4]), -1)
		if len(dates) != 2 {
			return nil, backtestProtocol("history date range missing")
		}
		e.StartDate, e.EndDate = dates[0], dates[1]
		e.CreatedAt = classText(row, "backtest-list__td_create-time")
		e.UsedTime, e.Frequency = normalizedText(cells[6]), normalizedText(cells[7])
		e.PythonVersion = classText(row, "backtest-list__td_python-version")
		capital := classText(row, "backtest-list__td_base-capital")
		e.InitialCapital, err = optionalNumberText(capital)
		if err != nil {
			return nil, backtestProtocol("invalid history initial capital")
		}
		out.Entries = append(out.Entries, e)
	}
	out.NextPage, err = nextHTMLPage(doc, page)
	return out, err
}

type AllBacktestOptions struct {
	Page, Limit int
	Keyword     string
}

type BacktestItem struct {
	BacktestID     string         `json:"backtestId"`
	Name           string         `json:"name"`
	InitialCapital OptionalNumber `json:"baseCapital"`
	CreatedAt      string         `json:"addTime"`
	StartTime      string         `json:"startTime"`
	EndTime        string         `json:"endTime"`
	Frequency      string         `json:"frequency"` // Display text, e.g. 每天.
	State          BacktestState  `json:"status"`
	StatusText     string         `json:"statusText"`
	PythonVersion  StringValue    `json:"version"`
}

type BacktestItemsPage struct {
	Page     IntValue       `json:"page"`
	Keyword  string         `json:"keyword"`
	Total    IntValue       `json:"total"`
	Entries  []BacktestItem `json:"list"`
	NextPage int            `json:"-"`
}

// ListAllBacktests is account-wide. The upstream ignores algorithmId, so this
// method deliberately has no algorithm filter. Resolve each ID with Backtest.
func (c *Client) ListAllBacktests(ctx context.Context, options AllBacktestOptions) (*BacktestItemsPage, error) {
	page, err := backtestPageNumber(options.Page)
	if err != nil {
		return nil, err
	}
	limit := options.Limit
	if limit < 0 {
		return nil, fmt.Errorf("negative page limit")
	}
	if limit == 0 {
		limit = 10
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(limit)}, "keyword": {options.Keyword}}
	p, err := getEnvelope[*BacktestItemsPage](ctx, c, "/algorithm/backtest/items", q)
	if err != nil {
		return nil, err
	}
	if p == nil || int(p.Page) != page || p.Total < 0 || p.Entries == nil {
		return nil, backtestProtocol("invalid account history page")
	}
	for _, entry := range p.Entries {
		if entry.BacktestID == "" || entry.State == "" {
			return nil, backtestProtocol("account history item missing identifiers")
		}
	}
	// Avoid multiplication overflow for caller-supplied limits/page numbers.
	if p.Total > 0 && page <= (int(p.Total)-1)/limit {
		if len(p.Entries) == 0 {
			return nil, backtestProtocol("empty account page before total")
		}
		p.NextPage = page + 1
	}
	return p, nil
}

type BacktestDetail struct {
	DetailID       string
	RunID          string
	SourceID       string
	AlgorithmID    string
	Name           string
	StartDate      string
	EndDate        string
	InitialCapital OptionalNumber
	Frequency      BacktestFrequency
	PythonVersion  string
}

func (c *Client) Backtest(ctx context.Context, detailID string) (*BacktestDetail, error) {
	q, err := backtestQuery(detailID)
	if err != nil {
		return nil, err
	}
	doc, err := c.backtestHTML(ctx, "/algorithm/backtest/detail", q)
	if err != nil {
		return nil, err
	}
	return parseBacktestDetail(doc, detailID)
}

func parseBacktestDetail(doc *html.Node, detailID string) (*BacktestDetail, error) {
	d := &BacktestDetail{DetailID: detailID, RunID: valueByID(doc, "backtestId"), AlgorithmID: valueByID(doc, "algorithmId"),
		Name: textByID(doc, "title-box"), StartDate: textByID(doc, "startDate"), EndDate: textByID(doc, "endDate"),
		Frequency: BacktestFrequency(valueByID(doc, "frequency")), PythonVersion: valueByID(doc, "tradePyVersion")}
	if source := elementByID(doc, "code"); source != nil {
		d.SourceID = nodeAttribute(source, "_backtestId")
	}
	if d.RunID == "" || d.StartDate == "" || d.EndDate == "" {
		return nil, backtestProtocol("detail identifiers or dates missing")
	}
	capital := strings.TrimSpace(strings.TrimLeft(textByID(doc, "baseCapital"), "￥¥"))
	var err error
	d.InitialCapital, err = optionalNumberText(capital)
	if err != nil {
		return nil, backtestProtocol("invalid initial capital")
	}
	return d, nil
}

func (c *Client) backtestHTML(ctx context.Context, path string, query url.Values) (*html.Node, error) {
	body, err := c.getHTML(ctx, path, query)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBodySize {
		return nil, backtestProtocol("HTML exceeds size limit")
	}
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return nil, backtestProtocol("invalid HTML")
	}
	return doc, nil
}

var backtestDatePattern = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)

func backtestPageNumber(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative page number")
	}
	if n == 0 {
		n = 1
	}
	return n, nil
}

func queryID(href, key string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}

func elementByID(root *html.Node, id string) *html.Node {
	return findElement(root, func(n *html.Node) bool { return nodeAttribute(n, "id") == id })
}

func hasNodeClass(n *html.Node, class string) bool {
	for _, value := range strings.Fields(nodeAttribute(n, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

func elementByClass(root *html.Node, class string) *html.Node {
	return findElement(root, func(n *html.Node) bool { return hasNodeClass(n, class) })
}

func valueByID(root *html.Node, id string) string {
	if n := elementByID(root, id); n != nil {
		return nodeAttribute(n, "value")
	}
	return ""
}

func textByID(root *html.Node, id string) string {
	if n := elementByID(root, id); n != nil {
		return normalizedText(n)
	}
	return ""
}

func classText(root *html.Node, class string) string {
	if n := elementByClass(root, class); n != nil {
		return normalizedText(n)
	}
	return ""
}

func optionalNumberText(s string) (OptionalNumber, error) {
	var result OptionalNumber
	err := result.UnmarshalJSON([]byte(strconv.Quote(s)))
	return result, err
}

func nextHTMLPage(doc *html.Node, current int) (int, error) {
	for _, a := range findElements(doc, func(n *html.Node) bool { return n.Data == "a" }) {
		isNext := nodeAttribute(a, "rel") == "next" || (a.Parent != nil && hasNodeClass(a.Parent, "next"))
		if !isNext || hasNodeClass(a, "disabled") || hasNodeClass(a, "nodisplay") || (a.Parent != nil && (hasNodeClass(a.Parent, "disabled") || hasNodeClass(a.Parent, "nodisplay"))) {
			continue
		}
		next, err := strconv.Atoi(queryID(nodeAttribute(a, "href"), "page"))
		if err != nil || next <= current {
			return 0, backtestProtocol("invalid next page link")
		}
		return next, nil
	}
	return 0, nil
}
