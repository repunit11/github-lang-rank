package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type repo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Fork     bool   `json:"fork"`
	Archived bool   `json:"archived"`
}

type langStat struct {
	Lang  string
	Bytes int64
}

func main() {
	configPath := &stringFlag{val: "config.json"}
	username := &stringFlag{}
	token := &stringFlag{}
	output := &stringFlag{}
	includeForks := &boolFlag{}
	includeArchived := &boolFlag{}
	useOrg := &boolFlag{}
	showOther := &boolFlag{}
	exclude := &stringFlag{}
	top := &intFlag{}

	flag.Var(configPath, "config", "Path to config JSON")
	flag.Var(username, "username", "GitHub username or org")
	flag.Var(token, "token", "GitHub token (optional)")
	flag.Var(output, "output", "Output path for SVG chart")
	flag.Var(includeForks, "include-forks", "Include forked repositories")
	flag.Var(includeArchived, "include-archived", "Include archived repositories")
	flag.Var(useOrg, "org", "Treat username as an org")
	flag.Var(showOther, "show-other", "Show aggregated Other bucket when top is used")
	flag.Var(exclude, "exclude", "Comma-separated languages to exclude")
	flag.Var(top, "top", "Limit to top N languages (0 = all)")
	flag.Parse()

	cfg, err := loadConfig(configPath.val)
	if err != nil {
		exitWith(err.Error())
	}

	merged := mergeConfig(cfg, username, token, output, includeForks, includeArchived, useOrg, showOther, exclude, top)

	if merged.Username == "" {
		exitWith("missing -username")
	}

	client := &http.Client{Timeout: 20 * time.Second}

	repos, err := fetchRepos(client, merged.Username, merged.Token, merged.Org)
	if err != nil {
		exitWith(err.Error())
	}

	filtered := make([]repo, 0, len(repos))
	for _, r := range repos {
		if !merged.IncludeForks && r.Fork {
			continue
		}
		if !merged.IncludeArchived && r.Archived {
			continue
		}
		filtered = append(filtered, r)
	}

	if len(filtered) == 0 {
		exitWith("no repositories after filtering")
	}

	total, err := fetchLanguages(client, filtered, merged.Token)
	if err != nil {
		exitWith(err.Error())
	}

	excluded := applyExcludes(total, merged.Exclude)
	ranked := rankLanguages(total)
	if merged.Top > 0 && merged.Top < len(ranked) {
		ranked = collapseOthers(ranked, merged.Top, *merged.ShowOther)
	}

	printTable(ranked)

	if err := writeSVG(merged.Output, ranked, merged.Username, excluded); err != nil {
		exitWith(err.Error())
	}
}

func exitWith(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}

func fetchRepos(client *http.Client, owner, token string, useOrg bool) ([]repo, error) {
	base := "https://api.github.com"
	endpoint := fmt.Sprintf("/users/%s/repos", owner)
	if useOrg {
		endpoint = fmt.Sprintf("/orgs/%s/repos", owner)
	}

	var all []repo
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s%s?per_page=100&page=%d", base, endpoint, page)
		var batch []repo
		if err := getJSON(client, url, token, &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
	}
	return all, nil
}

func fetchLanguages(client *http.Client, repos []repo, token string) (map[string]int64, error) {
	base := "https://api.github.com"
	total := make(map[string]int64)

	for _, r := range repos {
		url := fmt.Sprintf("%s/repos/%s/languages", base, r.FullName)
		var langs map[string]int64
		if err := getJSON(client, url, token, &langs); err != nil {
			return nil, fmt.Errorf("languages for %s: %w", r.FullName, err)
		}
		for lang, bytes := range langs {
			total[lang] += bytes
		}
	}

	return total, nil
}

func getJSON(client *http.Client, url, token string, target any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "github-lang-rank")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(target); err != nil {
		return err
	}
	return nil
}

func rankLanguages(total map[string]int64) []langStat {
	stats := make([]langStat, 0, len(total))
	for lang, bytes := range total {
		stats = append(stats, langStat{Lang: lang, Bytes: bytes})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Bytes == stats[j].Bytes {
			return stats[i].Lang < stats[j].Lang
		}
		return stats[i].Bytes > stats[j].Bytes
	})
	return stats
}

func printTable(ranked []langStat) {
	fmt.Println("Language Bytes")
	fmt.Println("-------- -----")
	for _, item := range ranked {
		fmt.Printf("%-8s %d\n", item.Lang, item.Bytes)
	}
}

func writeSVG(path string, ranked []langStat, owner string, excluded []string) error {
	if len(ranked) == 0 {
		return fmt.Errorf("no language data to chart")
	}

	const (
		width       = 640
		height      = 320
		cardPadding = 28
	)

	colors := []string{
		"#f2c94c",
		"#2d9cdb",
		"#27ae60",
		"#bb6bd9",
		"#56ccf2",
		"#eb5757",
	}

	totalBytes := int64(0)
	for _, item := range ranked {
		totalBytes += item.Bytes
	}
	if totalBytes == 0 {
		return fmt.Errorf("no language data to chart")
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&buf, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">`+"\n", width, height)
	buf.WriteString(`<rect width="100%" height="100%" rx="14" fill="#202a2f" stroke="#324047" stroke-width="2"/>` + "\n")
	if len(ranked) == 0 {
		return fmt.Errorf("no language data to chart")
	}

	cols := 3
	if len(ranked) < cols {
		cols = len(ranked)
	}
	rows := (len(ranked) + cols - 1) / cols

	headerHeight := 24
	headerGap := 18
	barHeight := 14
	barGap := 22
	tileHeight := 72
	tileGap := 16
	footerHeight := 0
	if len(excluded) > 0 {
		footerHeight = 28
	}
	gridHeight := rows*tileHeight + (rows-1)*tileGap
	contentHeight := headerHeight + headerGap + barHeight + barGap + gridHeight + footerHeight
	topOffset := (height - contentHeight) / 2
	if topOffset < cardPadding {
		topOffset = cardPadding
	}

	headerTextY := topOffset + 18
	fmt.Fprintf(&buf, `<text x="%d" y="%d" font-family="Poppins, 'Segoe UI', Arial, sans-serif" font-size="20" fill="#9be36a">Most Used Languages</text>`+"\n", cardPadding, headerTextY)

	barX := cardPadding
	barY := topOffset + headerHeight + headerGap
	barWidth := width - cardPadding*2
	fmt.Fprintf(&buf, `<rect x="%d" y="%d" width="%d" height="%d" rx="7" fill="#1b2328"/>`+"\n", barX, barY, barWidth, barHeight)
	fmt.Fprintf(&buf, `<clipPath id="barClip"><rect x="%d" y="%d" width="%d" height="%d" rx="7"/></clipPath>`+"\n", barX, barY, barWidth, barHeight)

	accumX := barX
	for i, item := range ranked {
		segmentWidth := int(float64(barWidth) * (float64(item.Bytes) / float64(totalBytes)))
		if segmentWidth == 0 {
			continue
		}
		if i == len(ranked)-1 {
			segmentWidth = barX + barWidth - accumX
		}
		color := colorForLanguage(item.Lang, colors[i%len(colors)])
		fmt.Fprintf(&buf, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s" clip-path="url(#barClip)"/>`+"\n", accumX, barY, segmentWidth, barHeight, color)
		accumX += segmentWidth
	}

	gridTop := barY + barHeight + barGap
	gridWidth := width - cardPadding*2
	tileWidth := (gridWidth - (cols-1)*tileGap) / cols

	for i, item := range ranked {
		row := i / cols
		col := i % cols
		x := cardPadding + col*(tileWidth+tileGap)
		y := gridTop + row*(tileHeight+tileGap)
		color := colorForLanguage(item.Lang, colors[i%len(colors)])
		percent := float64(item.Bytes) / float64(totalBytes) * 100

		fmt.Fprintf(&buf, `<rect x="%d" y="%d" width="%d" height="%d" rx="12" fill="#1b2328" stroke="#2c3a42" stroke-width="1"/>`+"\n", x, y, tileWidth, tileHeight)
		fmt.Fprintf(&buf, `<rect x="%d" y="%d" width="6" height="%d" rx="3" fill="%s"/>`+"\n", x+12, y+10, tileHeight-20, color)
		fmt.Fprintf(&buf, `<text x="%d" y="%d" font-family="Poppins, 'Segoe UI', Arial, sans-serif" font-size="16" fill="#d3dde3">%s</text>`+"\n", x+28, y+28, escapeText(item.Lang))
		fmt.Fprintf(&buf, `<text x="%d" y="%d" font-family="Poppins, 'Segoe UI', Arial, sans-serif" font-size="13" fill="#93a4ac">%.2f%%</text>`+"\n", x+28, y+48, percent)
		fmt.Fprintf(&buf, `<text x="%d" y="%d" font-family="Poppins, 'Segoe UI', Arial, sans-serif" font-size="12" fill="#6f848e">%d bytes</text>`+"\n", x+28, y+64, item.Bytes)
	}

	if len(excluded) > 0 {
		note := fmt.Sprintf("Excluded: %s", strings.Join(excluded, ", "))
		noteY := gridTop + gridHeight + 20
		if noteY > height-18 {
			noteY = height - 18
		}
		fmt.Fprintf(&buf, `<text x="%d" y="%d" font-family="Poppins, 'Segoe UI', Arial, sans-serif" font-size="12" fill="#93a4ac">%s</text>`+"\n", cardPadding, noteY, escapeText(note))
	}

	buf.WriteString(`</svg>` + "\n")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func escapeText(input string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(input)
}

func colorForLanguage(lang string, fallback string) string {
	if color, ok := languageColors[strings.ToLower(lang)]; ok {
		return color
	}
	return fallback
}

func initialForLanguage(lang string) string {
	for _, r := range lang {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if r >= 'a' && r <= 'z' {
				r = r - 'a' + 'A'
			}
			return string(r)
		}
	}
	return "?"
}

var languageColors = map[string]string{
	"go":         "#00ADD8",
	"python":     "#3572A5",
	"javascript": "#f1e05a",
	"typescript": "#3178c6",
	"java":       "#b07219",
	"php":        "#4F5D95",
	"ruby":       "#701516",
	"c":          "#555555",
	"c++":        "#f34b7d",
	"c#":         "#178600",
	"swift":      "#F05138",
	"kotlin":     "#A97BFF",
	"rust":       "#dea584",
	"scala":      "#c22d40",
	"shell":      "#89e051",
	"html":       "#e34c26",
	"css":        "#563d7c",
	"vue":        "#41b883",
	"dart":       "#00B4AB",
	"lua":        "#000080",
	"r":          "#198CE7",
	"matlab":     "#e16737",
	"makefile":   "#427819",
	"hcl":        "#844FBA",
	"dockerfile": "#384d54",
}

func collapseOthers(ranked []langStat, top int, showOther bool) []langStat {
	if top <= 0 || len(ranked) <= top {
		return ranked
	}
	keep := make([]langStat, 0, top+1)
	keep = append(keep, ranked[:top]...)
	if !showOther {
		return keep
	}
	var otherBytes int64
	for _, item := range ranked[top:] {
		otherBytes += item.Bytes
	}
	if otherBytes > 0 {
		keep = append(keep, langStat{Lang: "Other", Bytes: otherBytes})
	}
	return keep
}

type config struct {
	Username        string `json:"username"`
	Token           string `json:"token"`
	Output          string `json:"output"`
	IncludeForks    bool   `json:"include_forks"`
	IncludeArchived bool   `json:"include_archived"`
	Org             bool   `json:"org"`
	ShowOther       *bool  `json:"show_other"`
	Exclude         []string `json:"exclude"`
	Top             int    `json:"top"`
}

func loadConfig(path string) (config, error) {
	if path == "" {
		return config{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config{}, nil
		}
		return config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func mergeConfig(cfg config, username, token, output *stringFlag, includeForks, includeArchived, useOrg, showOther *boolFlag, exclude *stringFlag, top *intFlag) config {
	merged := cfg

	if username.set {
		merged.Username = username.val
	}
	if token.set {
		merged.Token = token.val
	}
	if output.set {
		merged.Output = output.val
	}
	if includeForks.set {
		merged.IncludeForks = includeForks.val
	}
	if includeArchived.set {
		merged.IncludeArchived = includeArchived.val
	}
	if useOrg.set {
		merged.Org = useOrg.val
	}
	if showOther.set {
		val := showOther.val
		merged.ShowOther = &val
	}
	if exclude.set {
		merged.Exclude = splitCSV(exclude.val)
	}
	if top.set {
		merged.Top = top.val
	}

	if merged.Output == "" {
		merged.Output = "lang-rank.svg"
	}
	if merged.ShowOther == nil {
		defaultOther := true
		merged.ShowOther = &defaultOther
	}

	return merged
}

type stringFlag struct {
	val string
	set bool
}

func (s *stringFlag) String() string { return s.val }

func (s *stringFlag) Set(value string) error {
	s.val = value
	s.set = true
	return nil
}

type boolFlag struct {
	val bool
	set bool
}

func (b *boolFlag) String() string { return fmt.Sprintf("%t", b.val) }

func (b *boolFlag) Set(value string) error {
	parsed, err := parseBool(value)
	if err != nil {
		return err
	}
	b.val = parsed
	b.set = true
	return nil
}

func (b *boolFlag) IsBoolFlag() bool { return true }

type intFlag struct {
	val int
	set bool
}

func (i *intFlag) String() string { return fmt.Sprintf("%d", i.val) }

func (i *intFlag) Set(value string) error {
	parsed, err := parseInt(value)
	if err != nil {
		return err
	}
	i.val = parsed
	i.set = true
	return nil
}

func parseBool(value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid bool: %s", value)
	}
	return parsed, nil
}

func parseInt(value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid int: %s", value)
	}
	return parsed, nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func applyExcludes(total map[string]int64, excludes []string) []string {
	if len(excludes) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(excludes))
	for _, item := range excludes {
		normalized[strings.ToLower(item)] = item
	}
	var removed []string
	for lang := range total {
		if original, ok := normalized[strings.ToLower(lang)]; ok {
			delete(total, lang)
			removed = append(removed, original)
		}
	}
	sort.Strings(removed)
	return removed
}
