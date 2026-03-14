package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/XferOps/winnow/internal/mcp"
	openai "github.com/sashabaranov/go-openai"
)

// Event types streamed to the client.
type EventType string

const (
	EventProgress EventType = "progress"
	EventComplete EventType = "complete"
	EventError    EventType = "error"
)

const (
	categoryTimeout = 20 * time.Second
	discoverTimeout = 30 * time.Second
	maxFilesPerCat  = 5 // max files the LLM may assign per category
)

// Event is a single SSE payload.
type Event struct {
	Type EventType
	Data interface{}
}

// ProgressData is sent while seeding is in progress.
//
// CurrentCategory is the single category being generated right now (generating step).
// DiscoveredCategories is the full list found by the LLM (discovering step, emitted once).
type ProgressData struct {
	Step                 string   `json:"step"`
	Message              string   `json:"message"`
	Current              int      `json:"current,omitempty"`
	Total                int      `json:"total,omitempty"`
	CurrentCategory      string   `json:"current_category,omitempty"`
	DiscoveredCategories []string `json:"discovered_categories,omitempty"`
}

// CompleteData is sent when seeding finishes successfully.
type CompleteData struct {
	ChunksWritten int      `json:"chunks_written"`
	Categories    []string `json:"categories"`
}

// ErrorData is sent when seeding fails.
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DiscoveredCategory is one category returned by the LLM discovery step.
type DiscoveredCategory struct {
	QueryKey    string   `json:"query_key"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
}

// Request is the input to Run.
type Request struct {
	RepoURL     string
	GitHubToken string
	ProjectID   string
}

type contextWriter interface {
	WriteContext(ctx context.Context, projectID string, in mcp.WriteContextInput) (*mcp.WriteContextResult, error)
}

var (
	fetchFileContentsFunc = fetchFileContents
	generateChunkFunc     = generateChunk
)

// Run orchestrates the full auto-seed pipeline, emitting Events to the returned channel.
// The channel is closed when seeding completes or errors.
func Run(ctx context.Context, tools *mcp.Tools, req Request) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		run(ctx, tools, req, ch)
	}()
	return ch
}

func emit(ch chan<- Event, t EventType, data interface{}) {
	ch <- Event{Type: t, Data: data}
}

func progress(ch chan<- Event, data ProgressData) {
	emit(ch, EventProgress, data)
}

func run(ctx context.Context, tools *mcp.Tools, req Request, ch chan<- Event) {
	// 1. Parse repo URL
	owner, repo, err := ParseRepoURL(req.RepoURL)
	if err != nil {
		emit(ch, EventError, ErrorData{Code: "INVALID_URL", Message: err.Error()})
		return
	}

	// 2. Fetch repo metadata
	progress(ch, ProgressData{Step: "fetching_repo", Message: fmt.Sprintf("Connecting to %s/%s...", owner, repo)})
	meta, err := FetchRepo(ctx, owner, repo, req.GitHubToken)
	if err != nil {
		if ghErr, ok := err.(*GitHubError); ok {
			emit(ch, EventError, ErrorData{Code: string(ghErr.Code), Message: ghErr.Message})
		} else {
			emit(ch, EventError, ErrorData{Code: "FETCH_ERROR", Message: err.Error()})
		}
		return
	}

	// 3. Fetch file tree
	progress(ch, ProgressData{Step: "fetching_tree", Message: "Fetching file tree..."})
	entries, err := FetchTree(ctx, meta, req.GitHubToken)
	if err != nil {
		if ghErr, ok := err.(*GitHubError); ok {
			emit(ch, EventError, ErrorData{Code: string(ghErr.Code), Message: ghErr.Message})
		} else {
			emit(ch, EventError, ErrorData{Code: "FETCH_ERROR", Message: err.Error()})
		}
		return
	}

	// 4. Filter entries (remove vendor, generated files, binaries)
	filtered := FilterEntries(entries)
	progress(ch, ProgressData{Step: "classifying", Message: fmt.Sprintf("Filtered to %d relevant files...", len(filtered))})
	if len(filtered) == 0 {
		emit(ch, EventError, ErrorData{
			Code:    "NO_FILES_FOUND",
			Message: "No recognisable source files found in this repo. Try the winnow-seed skill for manual seeding.",
		})
		return
	}

	// 5. LLM-driven category discovery
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		emit(ch, EventError, ErrorData{Code: "CONFIG_ERROR", Message: "OPENAI_API_KEY is not configured"})
		return
	}
	llm := openai.NewClient(apiKey)

	progress(ch, ProgressData{Step: "discovering", Message: "Identifying categories for this repo..."})
	discoverCtx, discoverCancel := context.WithTimeout(ctx, discoverTimeout)
	discovered, err := discoverCategories(discoverCtx, llm, meta, filtered)
	discoverCancel()
	if err != nil {
		emit(ch, EventError, ErrorData{
			Code:    "DISCOVERY_FAILED",
			Message: fmt.Sprintf("Failed to identify categories: %v", err),
		})
		return
	}

	// Broadcast the discovered category list so the UI can render pills immediately
	catKeys := make([]string, len(discovered))
	for i, c := range discovered {
		catKeys[i] = c.QueryKey
	}
	progress(ch, ProgressData{
		Step:                 "discovering",
		Message:              fmt.Sprintf("Found %d categories. Generating context...", len(discovered)),
		DiscoveredCategories: catKeys,
	})

	// 6. For each category: fetch file contents, call LLM, write chunk
	written := 0
	writtenCats := make([]string, 0, len(discovered))

	for i, cat := range discovered {
		current := i + 1
		total := len(discovered)

		progress(ch, ProgressData{
			Step:            "generating",
			Message:         fmt.Sprintf("Generating %s context... (%d/%d)", cat.QueryKey, current, total),
			Current:         current,
			Total:           total,
			CurrentCategory: cat.QueryKey,
		})

		categoryCtx, cancel := context.WithTimeout(ctx, categoryTimeout)
		ok, err := processCategory(categoryCtx, tools, req.ProjectID, meta, cat.QueryKey, cat.Description, cat.Files, req.GitHubToken, llm)
		cancel()
		if err != nil {
			log.Printf("seed: skipping category=%s repo=%s/%s: %v", cat.QueryKey, meta.Owner, meta.Repo, err)
			continue
		}
		if ok {
			written++
			writtenCats = append(writtenCats, cat.QueryKey)
		}
	}

	emit(ch, EventComplete, CompleteData{
		ChunksWritten: written,
		Categories:    writtenCats,
	})
}

// discoverCategories asks the LLM to identify the meaningful knowledge categories
// for this repo based on its filtered file tree. Returns 5-8 categories, each
// with a query_key slug, a one-sentence description, and up to 5 relevant file paths.
func discoverCategories(ctx context.Context, llm *openai.Client, meta *RepoMeta, entries []TreeEntry) ([]DiscoveredCategory, error) {
	// Build a condensed file list from paths only — no content needed here
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	fileList := strings.Join(paths, "\n")

	systemPrompt := `You are a technical analyst identifying knowledge categories for a codebase.
Return only valid JSON. No markdown fences, no explanation outside the JSON.`

	userPrompt := fmt.Sprintf(`Repository: %s/%s
%sPrimary language: %s

File tree:
%s

Identify 5-8 categories of knowledge an AI coding agent would need to understand this codebase.
Common categories include: architecture, domain-model, api-routes, auth, database-schema, code-patterns, deployment.
Use these if they fit. Define your own if the codebase calls for it (e.g. "training-pipeline", "ios-views", "smart-contracts").

For each category return:
- query_key: short kebab-case slug (e.g. "api-routes")
- description: one sentence describing what this category covers
- files: up to 5 most relevant file paths from the tree above (must exist in the tree exactly as listed)

Return a JSON array only:
[{"query_key":"...","description":"...","files":["..."]}]

If the repo has fewer than 5 meaningful categories, return fewer — quality over quantity.`,
		meta.Owner, meta.Repo,
		descriptionLine(meta),
		meta.Language,
		fileList,
	)

	resp, err := llm.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature: 0.1,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned no choices")
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	var cats []DiscoveredCategory
	if err := json.Unmarshal([]byte(raw), &cats); err != nil {
		return nil, fmt.Errorf("discovery output was not valid JSON: %w", err)
	}
	if len(cats) == 0 {
		return nil, fmt.Errorf("LLM returned no categories")
	}

	// Cap files per category to avoid over-fetching
	for i := range cats {
		if len(cats[i].Files) > maxFilesPerCat {
			cats[i].Files = cats[i].Files[:maxFilesPerCat]
		}
	}

	return cats, nil
}

func processCategory(
	ctx context.Context,
	writer contextWriter,
	projectID string,
	meta *RepoMeta,
	queryKey string,
	description string,
	files []string,
	githubToken string,
	llm *openai.Client,
) (bool, error) {
	contents, err := fetchFileContentsFunc(ctx, meta, files, githubToken)
	if err != nil {
		return false, fmt.Errorf("fetch file contents: %w", err)
	}
	if len(contents) == 0 {
		return false, nil
	}

	chunk, err := generateChunkFunc(ctx, llm, meta, queryKey, description, contents)
	if err != nil {
		return false, fmt.Errorf("generate chunk: %w", err)
	}

	_, err = writer.WriteContext(ctx, projectID, mcp.WriteContextInput{
		QueryKey: queryKey,
		Title:    chunk.Title,
		Content:  chunk.Content,
		Gotchas:  chunk.Gotchas,
		Related:  chunk.Related,
	})
	if err != nil {
		return false, fmt.Errorf("write context: %w", err)
	}

	return true, nil
}

// fetchFileContents fetches the content of each file path, returning a path→content map.
// Individual fetch errors are skipped silently (submodules, deleted files, etc.).
func fetchFileContents(ctx context.Context, meta *RepoMeta, files []string, token string) (map[string]string, error) {
	result := make(map[string]string, len(files))
	for _, path := range files {
		content, err := FetchFile(ctx, meta, path, token, maxFileBytes)
		if err != nil {
			continue
		}
		result[path] = content
	}
	return result, nil
}

// generatedChunk is the structured output from the LLM chunk generation step.
type generatedChunk struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Gotchas []string `json:"gotchas"`
	Related []string `json:"related"`
}

func generateChunk(ctx context.Context, llm *openai.Client, meta *RepoMeta, queryKey, description string, contents map[string]string) (*generatedChunk, error) {
	var sb strings.Builder
	for path, content := range contents {
		sb.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", path, content))
	}

	systemPrompt := `You are a technical knowledge extractor building a context base for AI coding agents.

Your output will be retrieved by agents that need to write, debug, and review code in this repository.
A great chunk lets an agent produce correct, idiomatic code immediately — without reading the source files.

RULES:
- Use structured markdown: headers, bullet lists, inline code, fenced code blocks.
- Be concrete: real function/method/type names, real signatures, real file paths, real SQL columns, real env var names.
- Include short code snippets that show the canonical pattern — not a description of the pattern.
- Do not write narrative prose ("The system uses..."). Write a technical reference ("## Auth\n- jwtMiddleware applied via...").
- Do not invent or speculate. Only document what is clearly present in the provided files.
- If the files don't contain enough substance for a useful chunk, return null.
- Return only valid JSON. No markdown fences around the JSON, no explanation outside it.`

	userPrompt := fmt.Sprintf(`Repository: %s/%s
%s

Category: %s
What this category covers: %s

Files:
%s

Return a JSON object with exactly these fields:
{
  "title": "precise title naming the specific thing, not just the category (max 80 chars)",
  "content": "structured markdown reference — headers, real names, real signatures, inline code and fenced snippets where they add clarity. An agent reading this should know exactly what to call and how.",
  "gotchas": ["specific things that will trip up an agent — e.g. 'WriteTimeout must be 0 for SSE or the handler is killed mid-stream'. No generic warnings."],
  "related": ["other category query_key names relevant to understanding this one"]
}

If the files don't contain enough substance for a useful chunk, return: null`,
		meta.Owner, meta.Repo,
		descriptionLine(meta),
		queryKey,
		description,
		sb.String(),
	)

	resp, err := llm.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4o,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned no choices")
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	if raw == "null" || raw == "" {
		return nil, fmt.Errorf("LLM indicated insufficient content for this category")
	}

	var chunk generatedChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		return nil, fmt.Errorf("LLM output was not valid JSON: %w", err)
	}
	if chunk.Title == "" || chunk.Content == "" {
		return nil, fmt.Errorf("LLM returned empty title or content")
	}

	return &chunk, nil
}

func descriptionLine(meta *RepoMeta) string {
	if meta.Description != "" {
		return fmt.Sprintf("Description: %s", meta.Description)
	}
	return ""
}
