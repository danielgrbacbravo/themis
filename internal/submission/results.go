package submission

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func normalize(raw string) string {
	switch strings.ToLower(raw) {
	case "passed", "accepted":
		return "passed"
	case "failed", "error", "compilerr", "runtimerr", "timeout", "outlimit", "diff", "deadline", "rejected":
		return "failed"
	case "queued", "pending":
		return "queued"
	case "running", "judging", "compiling", "processing", "busy":
		return "running"
	case "none", "unjudged", "uploaded":
		return "unjudged"
	default:
		return "unknown"
	}
}
func rawStatus(sel *goquery.Selection) string {
	classes := strings.Fields(sel.AttrOr("class", ""))
	for _, c := range classes {
		if normalize(c) != "unknown" {
			return c
		}
	}
	for _, c := range classes {
		if !contains([]string{"submission", "border", "fill", "round", "status-icon", "sub-casetop"}, c) {
			return c
		}
	}
	return "unknown"
}
func submissionAssignment(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	i := strings.Index(u.Path, "/@submissions/")
	if i < 0 {
		return ""
	}
	u.Path = "/course/" + strings.TrimPrefix(u.Path[:i], "/submission/")
	u.RawQuery = ""
	return u.String()
}
func LatestLink(doc *goquery.Document) string {
	var latest string
	doc.Find("section.status .cfg-line").Each(func(_ int, l *goquery.Selection) {
		key := strings.ToLower(strings.TrimSuffix(text(l.Find(".cfg-key")), ":"))
		if key == "latest" {
			latest = l.Find(".cfg-val a[href*='/submission/']").First().AttrOr("href", "")
		}
	})
	return latest
}
func (s *Service) FetchResult(ctx context.Context, raw string) (Result, error) {
	client, err := s.client(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrAuth, err)
	}
	ctx = context.WithValue(ctx, clientContextKey{}, client)
	doc, final, err := s.get(ctx, raw)
	if err != nil {
		return Result{}, err
	}
	if !concreteURL(final) {
		if latest := LatestLink(doc); latest != "" {
			doc, final, err = s.get(ctx, latest)
			if err != nil {
				return Result{}, err
			}
		}
	}
	r, err := s.ParseResult(doc, final)
	if err == nil && concreteURL(raw) {
		requested, _ := url.Parse(raw)
		resolved, _ := url.Parse(r.SubmissionURL)
		if requested.Path != resolved.Path {
			return Result{}, fmt.Errorf("response identifies a different submission")
		}
	}
	return r, err
}
func (s *Service) ParseResult(doc *goquery.Document, pageURL string) (Result, error) {
	sections := doc.Find("section.submission")
	var chosen *goquery.Selection
	if concreteURL(pageURL) {
		u, _ := url.Parse(pageURL)
		wanted := strings.TrimPrefix(u.Path, "/submission")
		sections.Each(func(_ int, sec *goquery.Selection) {
			if sec.AttrOr("id", "") == wanted || sec.AttrOr("data-watch", "") == wanted {
				chosen = sec
			}
		})
	} else if sections.Length() == 1 {
		chosen = sections.First()
	}
	if chosen == nil || chosen.Length() == 0 {
		return Result{}, fmt.Errorf("no matching submission result was found")
	}
	id := chosen.AttrOr("id", chosen.AttrOr("data-watch", ""))
	concrete, err := s.resolve("/submission" + id)
	if err != nil || !concreteURL(concrete) {
		return Result{}, fmt.Errorf("result does not identify a concrete submission")
	}
	r := Result{SubmissionURL: publicURL(concrete), AssignmentURL: submissionAssignment(concrete), RawStatus: rawStatus(chosen), UpdatedAt: time.Now().UTC(), Tests: []TestResult{}}
	r.Status = normalize(r.RawStatus)
	r.Title = text(chosen.Find(".sec-title a").First())
	r.Score = text(chosen.Find("text.percentage,.percentage").First())
	values := cfg(chosen)
	r.Group = values["group"]
	r.Message = values["status"]
	// Read only the latest numbered judgement; older attempts must not be merged.
	judgement := chosen.Find(".sub-cases").First()
	highest := -1
	number := regexp.MustCompile(`(?i)judg(?:e)?ment\s*#(\d+)`)
	chosen.Find(".sub-cases").Each(func(_ int, j *goquery.Selection) {
		match := number.FindStringSubmatch(text(j.Find(".subsec-title").First()))
		if len(match) > 1 {
			n, _ := strconv.Atoi(match[1])
			if n > highest {
				highest = n
				judgement = j
			}
		}
	})
	if judgement.Length() > 0 {
		judgement.Find("tr.sub-casetop").Each(func(_ int, row *goquery.Selection) {
			detail := row.AddSelection(row.NextUntil(".sub-casetop"))
			raw := rawStatus(row.Find("td.status-icon").First())
			if raw == "unknown" {
				raw = rawStatus(row)
			}
			tr := TestResult{Name: text(row.Find(".sub-casename strong").First()), RawStatus: raw, Status: normalize(raw), Message: text(row.Find("td.status-icon").First()), Hint: text(detail.Find("span[id^='show-hint-text-']"))}
			tr.Artifacts = s.artifacts(detail)
			r.Tests = append(r.Tests, tr)
			if tr.Status == "passed" || tr.Status == "failed" {
				r.Completed++
			}
		})
	}
	r.KnownTotal = len(r.Tests)
	outsideJudgements := chosen.Clone()
	outsideJudgements.Find(".sub-cases").Remove()
	r.Artifacts = s.artifacts(outsideJudgements)
	seen := map[string]bool{}
	for _, a := range r.Artifacts {
		seen[a.URL] = true
	}
	for _, a := range s.artifacts(judgement) {
		if !seen[a.URL] {
			r.Artifacts = append(r.Artifacts, a)
			seen[a.URL] = true
		}
	}
	// A failed compiler can leave every testcase as 'none'; the overall verdict
	// remains authoritative. Never derive the overall state from the score.
	return r, nil
}
func (s *Service) artifacts(sel *goquery.Selection) []Artifact {
	out := []Artifact{}
	seen := map[string]bool{}
	sel.Find("a[href*='/file/']").Each(func(_ int, a *goquery.Selection) {
		raw := a.AttrOr("href", "")
		if !strings.Contains(raw, "/output/") && !strings.Contains(raw, "/@tests/") && !strings.Contains(raw, "/%40tests/") {
			return
		}
		u, e := s.resolve(raw)
		if e != nil {
			return
		}
		u = publicURL(u)
		if seen[u] {
			return
		}
		seen[u] = true
		name := text(a)
		if name == "" {
			p, _ := url.Parse(u)
			parts := strings.Split(p.Path, "/")
			name = parts[len(parts)-1]
		}
		out = append(out, Artifact{Name: name, URL: u})
	})
	return out
}
