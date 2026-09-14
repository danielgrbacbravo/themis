package discovery

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	log "github.com/charmbracelet/log"
)

type Service struct {
	BaseURL  string
	Progress func(RefreshProgress)
}

type RefreshProgress struct {
	URL          string
	Title        string
	FetchedNodes int
}

type AssignmentEntry struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Depth     int    `json:"depth"`
	ParentURL string `json:"parent_url,omitempty"`
}

type AssignmentNode struct {
	Parent   *AssignmentNode
	Name     string
	URL      string
	children []*AssignmentNode
}

func NewService(baseURL string) *Service {
	return &Service{BaseURL: baseURL}
}

func (n *AssignmentNode) Title() string       { return n.Name }
func (n *AssignmentNode) Description() string { return n.URL }
func (n *AssignmentNode) FilterValue() string { return n.Name }

func (n *AssignmentNode) AppendChild(c *AssignmentNode, logger *log.Logger) {
	logger.Debug("Appending child", "child", c.Name, "parent", n.Name)
	c.Parent = n
	n.children = append(n.children, c)
}

func BuildAssignmentNode(parent *AssignmentNode, name string, assignmentURL string, logger *log.Logger) *AssignmentNode {
	logger.Debug("Building node", "name", name, "url", assignmentURL)
	return &AssignmentNode{
		Name:   name,
		URL:    assignmentURL,
		Parent: parent,
	}
}

func BuildRootAssignmentNode(name string, assignmentURL string, logger *log.Logger) *AssignmentNode {
	return BuildAssignmentNode(nil, name, assignmentURL, logger)
}

func (s *Service) PullAssignmentsAndBuildTree(client *http.Client, pageURL string, rootNode *AssignmentNode, depth int, logger *log.Logger) (*AssignmentNode, error) {
	assignments, err := s.getAssignmentsOnPage(client, pageURL)
	if err != nil {
		return nil, fmt.Errorf("error getting assignments on page: %w", err)
	}

	for _, assignment := range assignments {
		assignmentNode := BuildAssignmentNode(rootNode, assignment.Name, assignment.URL, logger)
		rootNode.AppendChild(assignmentNode, logger)
	}

	if depth <= 0 {
		return rootNode, nil
	}

	for _, child := range rootNode.children {
		_, err = s.PullAssignmentsAndBuildTree(client, child.URL, child, depth-1, logger)
		if err != nil {
			return nil, fmt.Errorf("error building tree: %w", err)
		}
	}

	return rootNode, nil
}

func (s *Service) DiscoverAssignments(client *http.Client, rootURL string, maxDepth int) (string, []AssignmentEntry, error) {
	if maxDepth < 0 {
		return "", nil, fmt.Errorf("--discover-depth must be >= 0")
	}

	normalizedRootURL, err := s.normalizeURL(rootURL)
	if err != nil {
		return "", nil, err
	}

	entries := make([]AssignmentEntry, 0)
	visited := make(map[string]bool)

	var walk func(currentURL string, parentURL string, depth int) error
	walk = func(currentURL string, parentURL string, depth int) error {
		if depth >= maxDepth {
			return nil
		}
		if visited[currentURL] {
			return nil
		}
		visited[currentURL] = true

		assignments, err := s.getAssignmentsOnPage(client, currentURL)
		if err != nil {
			return err
		}

		for _, assignment := range assignments {
			entries = append(entries, AssignmentEntry{
				Name:      assignment.Name,
				URL:       assignment.URL,
				Depth:     depth + 1,
				ParentURL: currentURL,
			})
			if err := walk(assignment.URL, currentURL, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(normalizedRootURL, "", 0); err != nil {
		return "", nil, fmt.Errorf("error discovering assignments: %w", err)
	}

	return normalizedRootURL, entries, nil
}

type assignmentRef struct {
	Name string
	URL  string
}

func (s *Service) getAssignmentsOnPage(client *http.Client, pageURL string) ([]assignmentRef, error) {
	resp, err := client.Get(pageURL)
	if err != nil {
		return nil, fmt.Errorf("error fetching page: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error parsing assignments page: %w", err)
	}

	assignments := make([]assignmentRef, 0)
	doc.Find("div.subsec.round.shade.ass-children ul.round li").Each(func(i int, sel *goquery.Selection) {
		anchor := sel.Find("span.ass-link a")
		assignmentName := strings.TrimSpace(anchor.Text())
		href, exists := anchor.Attr("href")
		if !exists {
			return
		}

		absoluteURL, resolveErr := s.resolveURL(pageURL, href)
		if resolveErr != nil {
			return
		}

		assignments = append(assignments, assignmentRef{
			Name: assignmentName,
			URL:  absoluteURL,
		})
	})

	return assignments, nil
}

func (s *Service) normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("root URL is empty")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid root URL: %w", err)
	}
	if parsed.IsAbs() {
		return raw, nil
	}

	baseParsed, err := url.Parse(s.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid service base URL: %w", err)
	}
	return baseParsed.ResolveReference(parsed).String(), nil
}

func (s *Service) resolveURL(currentURL string, href string) (string, error) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", fmt.Errorf("empty href")
	}

	parsedHref, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("invalid href: %w", err)
	}
	if parsedHref.IsAbs() {
		return parsedHref.String(), nil
	}

	baseParsed, err := url.Parse(currentURL)
	if err != nil {
		return "", fmt.Errorf("invalid current URL: %w", err)
	}
	return baseParsed.ResolveReference(parsedHref).String(), nil
}
