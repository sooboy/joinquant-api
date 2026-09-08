package joinquant

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

type StrategyFilter int

const (
	AllStrategies StrategyFilter = iota
	RunningStrategies
	StoppedStrategies
)

// Strategy contains the three identifiers exposed by the simulated-trading
// list. RunID and DetailID are opaque, short-lived web identifiers and must be
// refreshed by calling ListStrategies again. SpaceID is present for active
// strategy spaces and is the only identifier that should be treated as stable.
type Strategy struct {
	SpaceID   string
	RunID     string
	DetailID  string
	Name      string
	Frequency string
	Status    string
	StatusID  string
	StartDate string
	DetailURL string
}

func (c *Client) ListStrategies(ctx context.Context, filter StrategyFilter) ([]Strategy, error) {
	query := make(url.Values)
	switch filter {
	case AllStrategies:
	case RunningStrategies:
		query.Set("process", "1")
	case StoppedStrategies:
		query.Set("process", "0")
	default:
		return nil, fmt.Errorf("unknown strategy filter: %d", filter)
	}

	body, err := c.getHTML(ctx, "/algorithm/trade/list", query)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	document, err := html.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("%w: parse simulated-trading list: %v", ErrInvalidResponse, err)
	}
	table := findElement(document, func(node *html.Node) bool {
		return node.Data == "table" && nodeAttribute(node, "id") == "live_table"
	})
	if table == nil {
		return nil, fmt.Errorf("%w: simulated-trading table not found", ErrInvalidResponse)
	}

	rows := findElements(table, func(node *html.Node) bool { return node.Data == "tr" })
	strategies := make([]Strategy, 0, len(rows))
	for _, row := range rows {
		cells := directElementChildren(row, "td")
		if len(cells) < 5 {
			continue
		}
		link := findElement(cells[1], func(node *html.Node) bool { return node.Data == "a" })
		if link == nil {
			continue
		}
		detailURL := strings.TrimSpace(nodeAttribute(link, "href"))
		strategy := Strategy{
			SpaceID:   strings.TrimSpace(nodeAttribute(row, "data-backtestspaceid")),
			RunID:     strings.TrimSpace(nodeAttribute(cells[0], "_backtestid")),
			Name:      normalizedText(link),
			Frequency: trimCellLabel(normalizedText(cells[2]), "频率"),
			Status:    trimCellLabel(normalizedText(cells[3]), "状态"),
			StatusID:  strings.TrimSpace(nodeAttribute(row, "_status")),
			StartDate: normalizedText(cells[4]),
			DetailURL: detailURL,
		}
		if parsed, parseErr := url.Parse(detailURL); parseErr == nil {
			strategy.DetailID = parsed.Query().Get("backtestId")
		}
		if strategy.RunID == "" || strategy.Name == "" {
			continue
		}
		strategies = append(strategies, strategy)
	}
	return strategies, nil
}

// FindStrategy refreshes the list and returns the current opaque identifiers
// for a stable active strategy-space ID.
func (c *Client) FindStrategy(ctx context.Context, spaceID string) (*Strategy, error) {
	strategies, err := c.ListStrategies(ctx, AllStrategies)
	if err != nil {
		return nil, err
	}
	for i := range strategies {
		if strategies[i].SpaceID == spaceID {
			return &strategies[i], nil
		}
	}
	return nil, fmt.Errorf("strategy space %q not found", spaceID)
}

func nodeAttribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return attribute.Val
		}
	}
	return ""
}

func findElement(root *html.Node, predicate func(*html.Node) bool) *html.Node {
	if root.Type == html.ElementNode && predicate(root) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, predicate); found != nil {
			return found
		}
	}
	return nil
}

func findElements(root *html.Node, predicate func(*html.Node) bool) []*html.Node {
	var result []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && predicate(node) {
			result = append(result, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return result
}

func directElementChildren(root *html.Node, tag string) []*html.Node {
	var result []*html.Node
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag {
			result = append(result, child)
		}
	}
	return result
}

func normalizedText(root *html.Node) string {
	var builder strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
			builder.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return strings.Join(strings.FieldsFunc(builder.String(), unicode.IsSpace), " ")
}

func trimCellLabel(value, label string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, label)
	return strings.TrimSpace(value)
}
