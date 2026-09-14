package state

import "time"

const CurrentSchemaVersion = 1

type Status string

const (
	StatusNever Status = "never"
	StatusOK    Status = "ok"
	StatusStale Status = "stale"
	StatusError Status = "error"
)

type State struct {
	SchemaVersion  int             `json:"schema_version"`
	BaseURL        string          `json:"base_url"`
	CatalogRootURL string          `json:"catalog_root_url"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Roots          []RootRef       `json:"roots"`
	Nodes          map[string]Node `json:"nodes"`
}

type RootRef struct {
	NodeID       string    `json:"node_id"`
	CanonicalURL string    `json:"canonical_url"`
	Title        string    `json:"title,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Node struct {
	ID               string         `json:"id"`
	Kind             string         `json:"kind"`
	Title            string         `json:"title"`
	CanonicalURL     string         `json:"canonical_url"`
	NavAPIURL        string         `json:"nav_api_url,omitempty"`
	ParentIDs        []string       `json:"parent_ids"`
	ChildIDs         []string       `json:"child_ids"`
	ChildrenHydrated bool           `json:"children_hydrated"`
	DepthHint        int            `json:"depth_hint"`
	Status           Status         `json:"status"`
	LastFetchedAt    *time.Time     `json:"last_fetched_at,omitempty"`
	LastSuccessAt    *time.Time     `json:"last_success_at,omitempty"`
	LastError        string         `json:"last_error,omitempty"`
	ContentHash      string         `json:"content_hash,omitempty"`
	Details          map[string]any `json:"details,omitempty"`
	Assets           []AssetRef     `json:"assets"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type AssetRef struct {
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Path      string `json:"path,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type CourseSummary struct {
	RootNodeID           string
	TotalAssignments     int
	SubmittedAssignments int
	PassedAssignments    int
	CompletionPercent    int
	UpcomingDeadlines    []DeadlineGroup
	OverdueAssignments   []AssignmentDeadline
}

type DeadlineGroup struct {
	DaysUntil   int
	DueDate     string
	Assignments []AssignmentDeadline
}

type AssignmentDeadline struct {
	NodeID     string
	Title      string
	URL        string
	DueAt      time.Time
	DueDisplay string
	Status     string
	Submitted  bool
	Passed     bool
}
