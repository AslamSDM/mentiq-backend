package main

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
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

func (s *Server) crawlBrandProfileHandler(c *gin.Context) {
	var req BrandProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	url := req.WebsiteURL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	profile, err := crawlWebsite(url)
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

func crawlWebsite(url string) (*BrandProfile, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("User-Agent", "MentiQ-BrandCrawler/1.0")
	req.Header.Set("Accept", "text/html")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	html := string(body)
	profile := &BrandProfile{WebsiteURL: url}

	profile.CompanyName = extractMeta(html, "og:site_name")
	if profile.CompanyName == "" {
		profile.CompanyName = extractTitle(html)
	}

	profile.Description = extractMeta(html, "og:description")
	if profile.Description == "" {
		profile.Description = extractMeta(html, "description")
	}

	profile.Tagline = extractMeta(html, "og:title")

	profile.Colors = extractColors(html)

	return profile, nil
}

func extractTitle(html string) string {
	re := regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	match := re.FindStringSubmatch(html)
	if len(match) > 1 {
		title := strings.TrimSpace(match[1])
		if idx := strings.Index(title, " | "); idx > 0 {
			return strings.TrimSpace(title[:idx])
		}
		if idx := strings.Index(title, " - "); idx > 0 {
			return strings.TrimSpace(title[:idx])
		}
		return title
	}
	return ""
}

func extractMeta(html, name string) string {
	patterns := []string{
		fmt.Sprintf(`(?i)<meta[^>]*(?:property|name)="%s"[^>]*content="([^"]*)"`, regexp.QuoteMeta(name)),
		fmt.Sprintf(`(?i)<meta[^>]*content="([^"]*)"[^>]*(?:property|name)="%s"`, regexp.QuoteMeta(name)),
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(html)
		if len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func extractColors(html string) []string {
	re := regexp.MustCompile(`#[0-9a-fA-F]{6}`)
	matches := re.FindAllString(html, -1)

	seen := make(map[string]int)
	for _, color := range matches {
		lower := strings.ToLower(color)
		if lower == "#000000" || lower == "#ffffff" || lower == "#333333" {
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
	limit := 5
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for i := 0; i < limit; i++ {
		result = append(result, sorted[i].color)
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
