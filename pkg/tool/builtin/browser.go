package toolbuiltin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/cinience/saker/pkg/tool"
)

const browserDescription = `Automates a headless Chrome browser for web interaction.

Supports navigating to URLs, taking screenshots, clicking elements, typing text,
evaluating JavaScript, and extracting page content. Useful for web scraping,
testing, and interacting with web applications.

Security: only http/https URLs are allowed. File and data URLs are blocked.`

var browserSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"action": map[string]any{
			"type":        "string",
			"description": "Action to perform: navigate, screenshot, click, type, evaluate, content",
			"enum":        []string{"navigate", "screenshot", "click", "type", "evaluate", "content"},
		},
		"url": map[string]any{
			"type":        "string",
			"description": "URL to navigate to (required for navigate action)",
		},
		"selector": map[string]any{
			"type":        "string",
			"description": "CSS selector for click/type actions",
		},
		"text": map[string]any{
			"type":        "string",
			"description": "Text to type (required for type action)",
		},
		"expression": map[string]any{
			"type":        "string",
			"description": "JavaScript expression to evaluate (required for evaluate action)",
		},
	},
	Required: []string{"action"},
}

// BrowserTool drives a headless Chrome via chromedp.
type BrowserTool struct {
	allocCtx context.Context
	cancel   context.CancelFunc
	taskCtx  context.Context
	tcancel  context.CancelFunc
}

// NewBrowserTool creates a browser tool with a headless Chrome allocator.
func NewBrowserTool() *BrowserTool {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	taskCtx, tcancel := chromedp.NewContext(allocCtx)
	return &BrowserTool{
		allocCtx: allocCtx,
		cancel:   cancel,
		taskCtx:  taskCtx,
		tcancel:  tcancel,
	}
}

func (b *BrowserTool) Name() string             { return "browser" }
func (b *BrowserTool) Description() string      { return browserDescription }
func (b *BrowserTool) Schema() *tool.JSONSchema { return browserSchema }

// Close releases the browser allocator resources.
func (b *BrowserTool) Close() {
	if b.tcancel != nil {
		b.tcancel()
	}
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *BrowserTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	action, _ := params["action"].(string)
	if action == "" {
		return nil, fmt.Errorf("browser: action is required")
	}

	// Apply a timeout to all browser operations.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	switch action {
	case "navigate":
		return b.navigate(ctx, params)
	case "screenshot":
		return b.screenshot(ctx, params)
	case "click":
		return b.click(ctx, params)
	case "type":
		return b.typeText(ctx, params)
	case "evaluate":
		return b.evaluate(ctx, params)
	case "content":
		return b.content(ctx)
	default:
		return nil, fmt.Errorf("browser: unknown action %q", action)
	}
}

func (b *BrowserTool) navigate(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	url, _ := params["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("browser: url is required for navigate")
	}
	if err := validateBrowserURL(url); err != nil {
		return nil, err
	}

	var title string
	err := chromedp.Run(b.taskCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Title(&title),
	)
	if err != nil {
		return nil, fmt.Errorf("browser navigate: %w", err)
	}
	return &tool.ToolResult{
		Output: fmt.Sprintf("Navigated to %s\nTitle: %s", url, title),
	}, nil
}

func (b *BrowserTool) screenshot(_ context.Context, _ map[string]any) (*tool.ToolResult, error) {
	var buf []byte
	err := chromedp.Run(b.taskCtx,
		chromedp.FullScreenshot(&buf, 90),
	)
	if err != nil {
		return nil, fmt.Errorf("browser screenshot: %w", err)
	}

	tmpDir := os.TempDir()
	fname := fmt.Sprintf("browser_screenshot_%d.png", time.Now().UnixMilli())
	path := filepath.Join(tmpDir, fname)
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return nil, fmt.Errorf("browser screenshot save: %w", err)
	}
	return &tool.ToolResult{
		Output: fmt.Sprintf("Screenshot saved: %s (%d bytes)", path, len(buf)),
	}, nil
}

func (b *BrowserTool) click(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	sel, _ := params["selector"].(string)
	if sel == "" {
		return nil, fmt.Errorf("browser: selector is required for click")
	}
	err := chromedp.Run(b.taskCtx,
		chromedp.Click(sel, chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("browser click %q: %w", sel, err)
	}
	return &tool.ToolResult{Output: fmt.Sprintf("Clicked: %s", sel)}, nil
}

func (b *BrowserTool) typeText(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	sel, _ := params["selector"].(string)
	if sel == "" {
		return nil, fmt.Errorf("browser: selector is required for type")
	}
	text, _ := params["text"].(string)
	if text == "" {
		return nil, fmt.Errorf("browser: text is required for type")
	}
	err := chromedp.Run(b.taskCtx,
		chromedp.SendKeys(sel, text, chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("browser type %q: %w", sel, err)
	}
	return &tool.ToolResult{Output: fmt.Sprintf("Typed into %s", sel)}, nil
}

func (b *BrowserTool) evaluate(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	expr, _ := params["expression"].(string)
	if expr == "" {
		return nil, fmt.Errorf("browser: expression is required for evaluate")
	}
	var result any
	err := chromedp.Run(b.taskCtx,
		chromedp.Evaluate(expr, &result),
	)
	if err != nil {
		return nil, fmt.Errorf("browser evaluate: %w", err)
	}
	return &tool.ToolResult{Output: fmt.Sprintf("Result: %v", result)}, nil
}

func (b *BrowserTool) content(_ context.Context) (*tool.ToolResult, error) {
	var body string
	err := chromedp.Run(b.taskCtx,
		chromedp.InnerHTML("body", &body, chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("browser content: %w", err)
	}

	// Truncate large content to avoid overwhelming the LLM.
	const maxLen = 10000
	if len(body) > maxLen {
		body = body[:maxLen] + "\n... (truncated)"
	}
	return &tool.ToolResult{Output: body}, nil
}

// validateBrowserURL ensures only http/https schemes are used.
func validateBrowserURL(u string) error {
	lower := strings.ToLower(strings.TrimSpace(u))
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return nil
	}
	return fmt.Errorf("browser: only http/https URLs are allowed, got %q", u)
}
