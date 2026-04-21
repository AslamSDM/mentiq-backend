package main

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type BrandProfileRequest struct {
	WebsiteURL string `json:"website_url" binding:"required"`
}

type BrandProfile struct {
	CompanyName string   `json:"company_name"`
	Tagline     string   `json:"tagline"`
	Description string   `json:"description"`
	Colors      []string `json:"colors"`
	Industry    string   `json:"industry"`
	WebsiteURL  string   `json:"website_url"`
}

var crawlClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

func (s *Server) crawlBrandProfileHandler(c *gin.Context) {
	var req BrandProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	siteURL := req.WebsiteURL
	if !strings.HasPrefix(siteURL, "http://") && !strings.HasPrefix(siteURL, "https://") {
		siteURL = "https://" + siteURL
	}

	profile, err := crawlWebsite(siteURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to crawl website: %v", err)})
		return
	}

	if s.llmService != nil {
		enriched, err := s.enrichBrandProfileWithLLM(profile)
		if err == nil {
			profile = enriched
		}
	}

	c.JSON(http.StatusOK, profile)
}

func fetchPage(pageURL string) (string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/css,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := crawlClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func crawlWebsite(siteURL string) (*BrandProfile, error) {
	pageHTML, err := fetchPage(siteURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	profile := &BrandProfile{
		WebsiteURL: siteURL,
		Colors:     []string{},
	}

	// === Meta extraction (works for SSR Next.js + any site with SEO tags) ===
	profile.CompanyName = extractMeta(pageHTML, "og:site_name")
	if profile.CompanyName == "" {
		profile.CompanyName = extractMeta(pageHTML, "application-name")
	}
	if profile.CompanyName == "" {
		profile.CompanyName = extractTitle(pageHTML)
	}

	profile.Description = extractMeta(pageHTML, "og:description")
	if profile.Description == "" {
		profile.Description = extractMeta(pageHTML, "description")
	}

	profile.Tagline = extractMeta(pageHTML, "og:title")

	// === Color extraction ===
	// 1. theme-color meta (reliable, set by most modern sites)
	themeColor := extractMeta(pageHTML, "theme-color")
	if themeColor != "" && strings.HasPrefix(themeColor, "#") {
		profile.Colors = append(profile.Colors, strings.ToLower(themeColor))
	}
	// Also check msapplication-TileColor
	tileColor := extractMeta(pageHTML, "msapplication-TileColor")
	if tileColor != "" && strings.HasPrefix(tileColor, "#") {
		profile.Colors = append(profile.Colors, strings.ToLower(tileColor))
	}

	// 2. CSS custom properties from inline <style> tags (Next.js inlines CSS modules)
	profile.Colors = append(profile.Colors, extractCSSVariableColors(pageHTML)...)

	// 3. Inline hex colors from HTML
	profile.Colors = append(profile.Colors, extractColors(pageHTML)...)

	// 4. Fetch linked stylesheets in parallel (catches external CSS in React/Next apps)
	cssColors := fetchLinkedStylesheetColors(pageHTML, siteURL)
	profile.Colors = append(profile.Colors, cssColors...)

	// 5. Extract from Next.js __NEXT_DATA__ if present
	nextData := extractNextData(pageHTML)
	if nextData != "" {
		profile.Colors = append(profile.Colors, extractColors(nextData)...)
		// Sometimes page props have brand info
		if profile.CompanyName == "" {
			profile.CompanyName = extractJSONField(nextData, "companyName", "company_name", "siteName", "site_name", "brandName", "brand_name")
		}
	}

	profile.Colors = deduplicateColors(profile.Colors, 5)

	return profile, nil
}

// extractTitle handles multiline <title> tags
func extractTitle(pageHTML string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	match := re.FindStringSubmatch(pageHTML)
	if len(match) > 1 {
		title := strings.Join(strings.Fields(match[1]), " ")
		title = html.UnescapeString(title)
		for _, sep := range []string{" | ", " - ", " — ", " · ", " : "} {
			if idx := strings.Index(title, sep); idx > 0 {
				return strings.TrimSpace(title[:idx])
			}
		}
		return title
	}
	return ""
}

// extractMeta handles both double and single quoted attributes in either order
func extractMeta(pageHTML, name string) string {
	patterns := []string{
		fmt.Sprintf(`(?i)<meta[^>]*(?:property|name)=["\']%s["\'][^>]*content=["\']([^"\']*)["\']`, regexp.QuoteMeta(name)),
		fmt.Sprintf(`(?i)<meta[^>]*content=["\']([^"\']*)["\'][^>]*(?:property|name)=["\']%s["\']`, regexp.QuoteMeta(name)),
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(pageHTML)
		if len(match) > 1 {
			return html.UnescapeString(strings.TrimSpace(match[1]))
		}
	}
	return ""
}

// extractCSSVariableColors pulls brand-related CSS custom properties from inline <style> tags
// e.g. --primary: #3b82f6; --brand-color: #ef4444;
func extractCSSVariableColors(pageHTML string) []string {
	var colors []string

	// Extract all inline <style> content
	styleRe := regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	styleMatches := styleRe.FindAllStringSubmatch(pageHTML, -1)

	brandVarRe := regexp.MustCompile(`(?i)--(primary|brand|accent|main|theme|color-primary|color-brand|color-accent)[^:]*:\s*(#[0-9a-fA-F]{3,8})`)

	for _, m := range styleMatches {
		css := m[1]
		varMatches := brandVarRe.FindAllStringSubmatch(css, -1)
		for _, vm := range varMatches {
			color := strings.ToLower(vm[2])
			if len(color) == 4 {
				// Expand shorthand #abc -> #aabbcc
				color = "#" + string(color[1]) + string(color[1]) + string(color[2]) + string(color[2]) + string(color[3]) + string(color[3])
			}
			colors = append(colors, color)
		}
	}

	return colors
}

// fetchLinkedStylesheetColors fetches up to 3 linked CSS files and extracts colors
func fetchLinkedStylesheetColors(pageHTML, baseURL string) []string {
	linkRe := regexp.MustCompile(`(?i)<link[^>]*rel=["\']stylesheet["\'][^>]*href=["\']([^"\']+)["\']`)
	linkRe2 := regexp.MustCompile(`(?i)<link[^>]*href=["\']([^"\']+)["\'][^>]*rel=["\']stylesheet["\']`)

	var cssURLs []string
	seen := make(map[string]bool)

	for _, re := range []*regexp.Regexp{linkRe, linkRe2} {
		matches := re.FindAllStringSubmatch(pageHTML, -1)
		for _, m := range matches {
			href := m[1]
			if seen[href] {
				continue
			}
			seen[href] = true
			cssURLs = append(cssURLs, href)
		}
	}

	// Cap at 3 to avoid slowness
	if len(cssURLs) > 3 {
		cssURLs = cssURLs[:3]
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	var allColors []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, href := range cssURLs {
		absoluteURL := resolveURL(parsed, href)
		if absoluteURL == "" {
			continue
		}

		wg.Add(1)
		go func(cssURL string) {
			defer wg.Done()
			css, err := fetchPage(cssURL)
			if err != nil {
				return
			}
			colors := extractColors(css)
			cssVarColors := extractCSSVarColorsFromCSS(css)

			mu.Lock()
			allColors = append(allColors, cssVarColors...)
			allColors = append(allColors, colors...)
			mu.Unlock()
		}(absoluteURL)
	}

	wg.Wait()
	return allColors
}

// extractCSSVarColorsFromCSS pulls brand CSS vars from raw CSS content
func extractCSSVarColorsFromCSS(css string) []string {
	var colors []string
	brandVarRe := regexp.MustCompile(`(?i)--(primary|brand|accent|main|theme|color-primary|color-brand|color-accent)[^:]*:\s*(#[0-9a-fA-F]{3,8})`)
	matches := brandVarRe.FindAllStringSubmatch(css, -1)
	for _, m := range matches {
		color := strings.ToLower(m[2])
		if len(color) == 4 {
			color = "#" + string(color[1]) + string(color[1]) + string(color[2]) + string(color[2]) + string(color[3]) + string(color[3])
		}
		colors = append(colors, color)
	}
	return colors
}

func resolveURL(base *url.URL, href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if strings.HasPrefix(href, "//") {
		return base.Scheme + ":" + href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

// extractNextData parses Next.js __NEXT_DATA__ script for additional context
func extractNextData(pageHTML string) string {
	re := regexp.MustCompile(`(?s)<script\s+id="__NEXT_DATA__"\s+type="application/json">(.*?)</script>`)
	match := re.FindStringSubmatch(pageHTML)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

// extractJSONField does simple regex extraction for key:"value" patterns in JSON
func extractJSONField(json string, keys ...string) string {
	for _, key := range keys {
		re := regexp.MustCompile(fmt.Sprintf(`(?i)"%s"\s*:\s*"([^"]+)"`, regexp.QuoteMeta(key)))
		match := re.FindStringSubmatch(json)
		if len(match) > 1 {
			return match[1]
		}
	}
	return ""
}

func extractColors(content string) []string {
	re := regexp.MustCompile(`#[0-9a-fA-F]{6}`)
	matches := re.FindAllString(content, -1)

	neutrals := map[string]bool{
		"#000000": true, "#ffffff": true, "#333333": true,
		"#666666": true, "#999999": true, "#cccccc": true,
		"#111111": true, "#222222": true, "#f5f5f5": true,
		"#eeeeee": true, "#dddddd": true, "#fafafa": true,
		"#f8f8f8": true, "#e5e5e5": true, "#d4d4d4": true,
		"#a3a3a3": true, "#737373": true, "#525252": true,
		"#404040": true, "#262626": true, "#171717": true,
	}

	seen := make(map[string]int)
	for _, color := range matches {
		lower := strings.ToLower(color)
		if neutrals[lower] {
			continue
		}
		seen[lower]++
	}

	type colorCount struct {
		color string
		count int
	}
	var sorted []colorCount
	for c, n := range seen {
		sorted = append(sorted, colorCount{c, n})
	}

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var result []string
	for i := 0; i < len(sorted) && i < 10; i++ {
		result = append(result, sorted[i].color)
	}
	return result
}

func deduplicateColors(colors []string, max int) []string {
	seen := make(map[string]bool)
	var result []string
	for _, c := range colors {
		lower := strings.ToLower(c)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, lower)
		}
		if len(result) >= max {
			break
		}
	}
	if result == nil {
		return []string{}
	}
	return result
}

func (s *Server) enrichBrandProfileWithLLM(profile *BrandProfile) (*BrandProfile, error) {
	prompt := fmt.Sprintf(`Based on this website data, provide a brief brand analysis.

Website: %s
Title/Company: %s
Description: %s
Tagline: %s
Brand Colors: %s

Respond in this exact format (one line each):
COMPANY: <company name>
INDUSTRY: <industry/sector in 2-3 words>
DESCRIPTION: <one-sentence brand description>

Keep responses concise.`, profile.WebsiteURL, profile.CompanyName, profile.Description, profile.Tagline, strings.Join(profile.Colors, ", "))

	response, err := s.llmService.generateContent(prompt)
	if err != nil {
		return profile, err
	}

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "COMPANY:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "COMPANY:"))
			if val != "" {
				profile.CompanyName = val
			}
		} else if strings.HasPrefix(line, "INDUSTRY:") {
			profile.Industry = strings.TrimSpace(strings.TrimPrefix(line, "INDUSTRY:"))
		} else if strings.HasPrefix(line, "DESCRIPTION:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "DESCRIPTION:"))
			if val != "" {
				profile.Description = val
			}
		}
	}

	return profile, nil
}
