package jina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"tourplannerbot/internal/tools"
)

const (
	readURLToolName     = "read_url"
	maximumLinkCount    = 50
	maximumLinkTextSize = 120
)

// ReadURLTool retrieves one web page through the Jina Reader API and returns it
// as Markdown the model can quote from.
type ReadURLTool struct {
	client             *Client
	maximumContentSize int
}

// jinaReaderEnvelope contains only the Reader response fields the tool
// forwards. Jina also returns page metadata, favicon data, and token usage that
// the model has no use for.
type jinaReaderEnvelope struct {
	Code int `json:"code"`
	Data struct {
		Title         string            `json:"title"`
		Description   string            `json:"description"`
		URL           string            `json:"url"`
		Content       string            `json:"content"`
		PublishedTime string            `json:"publishedTime"`
		Warning       string            `json:"warning"`
		Links         map[string]string `json:"links"`
		HTTPStatus    int               `json:"httpStatus"`
	} `json:"data"`
}

// pageLink is one outgoing link offered to the model so it can continue
// navigating from the retrieved page.
type pageLink struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// NewReadURL creates the Jina page reader tool and validates its content limit.
func NewReadURL(client *Client, maximumContentSize int) (*ReadURLTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Jina client is required")
	}
	if maximumContentSize <= 0 {
		return nil, fmt.Errorf("Jina maximum content size must be positive")
	}
	return &ReadURLTool{client: client, maximumContentSize: maximumContentSize}, nil
}

// Definition describes the read_url function to the LLM.
func (readURLTool *ReadURLTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        readURLToolName,
		Description: "Open one web page and read its content as Markdown. Use it after a web search to read a promising result, or whenever the user supplies a link. It renders JavaScript, so it also works on pages a plain fetch cannot read. Set with_links to true when you may need to follow the page's own navigation, such as moving from a venue's home page to its opening hours or prices.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The absolute HTTP or HTTPS address of the page to read.",
				},
				"with_links": map[string]any{
					"type":        "boolean",
					"description": "Optional. When true, also return the outgoing links found on the page so you can navigate further. Defaults to false.",
				},
			},
			"required":             []string{"url"},
			"additionalProperties": false,
		},
		Strict: false,
		Source: "jina",
	}
}

// Execute validates the requested address, retrieves the page through Jina, and
// returns a compact JSON object containing its readable Markdown content.
func (readURLTool *ReadURLTool) Execute(applicationContext context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		URL       string `json:"url"`
		WithLinks *bool  `json:"with_links"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", readURLToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", readURLToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", readURLToolName, err)
	}

	requestedURL, err := validatePageURL(arguments.URL)
	if err != nil {
		return "", err
	}
	linksRequested := arguments.WithLinks != nil && *arguments.WithLinks

	additionalHeaders := map[string]string{}
	if linksRequested {
		additionalHeaders["X-With-Links-Summary"] = "true"
	}
	responseBody, err := readURLTool.client.postJSON(applicationContext, map[string]string{"url": requestedURL}, additionalHeaders)
	if err != nil {
		return "", err
	}

	var responseEnvelope jinaReaderEnvelope
	if err := json.Unmarshal(responseBody, &responseEnvelope); err != nil {
		return "", fmt.Errorf("decode Jina Reader response: %w", err)
	}

	pageContent := strings.TrimSpace(responseEnvelope.Data.Content)
	contentTruncated := false
	if len(pageContent) > readURLTool.maximumContentSize {
		pageContent = truncateOnUTF8Boundary(pageContent, readURLTool.maximumContentSize)
		contentTruncated = true
	}

	resolvedURL := strings.TrimSpace(responseEnvelope.Data.URL)
	if resolvedURL == "" {
		resolvedURL = requestedURL
	}

	result := struct {
		URL           string     `json:"url"`
		Title         string     `json:"title,omitempty"`
		Description   string     `json:"description,omitempty"`
		PublishedTime string     `json:"published_time,omitempty"`
		HTTPStatus    int        `json:"http_status,omitempty"`
		Content       string     `json:"content"`
		Truncated     bool       `json:"truncated"`
		Links         []pageLink `json:"links,omitempty"`
		Warning       string     `json:"warning,omitempty"`
	}{
		URL:           resolvedURL,
		Title:         strings.TrimSpace(responseEnvelope.Data.Title),
		Description:   strings.TrimSpace(responseEnvelope.Data.Description),
		PublishedTime: strings.TrimSpace(responseEnvelope.Data.PublishedTime),
		HTTPStatus:    responseEnvelope.Data.HTTPStatus,
		Content:       pageContent,
		Truncated:     contentTruncated,
		Warning:       firstLine(strings.TrimSpace(responseEnvelope.Data.Warning)),
	}
	if linksRequested {
		result.Links = compactLinks(responseEnvelope.Data.Links)
	}
	if pageContent == "" {
		// A reachable but empty page is reported explicitly. Otherwise the
		// model cannot tell it apart from a page it simply failed to quote.
		result.Content = ""
		if result.Warning == "" {
			result.Warning = "The page was retrieved but contained no readable text."
		}
	}

	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode %s result: %w", readURLToolName, err)
	}
	return string(encodedResult), nil
}

// validatePageURL rejects anything that is not an absolute HTTP or HTTPS page
// address, so the tool cannot be steered into other URL schemes.
func validatePageURL(rawURL string) (string, error) {
	trimmedURL := strings.TrimSpace(rawURL)
	if trimmedURL == "" {
		return "", fmt.Errorf("url is required")
	}
	parsedURL, err := url.ParseRequestURI(trimmedURL)
	if err != nil {
		return "", fmt.Errorf("url must be an absolute HTTP or HTTPS address")
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("url must use the http or https scheme, got %q", parsedURL.Scheme)
	}
	if parsedURL.Host == "" {
		return "", fmt.Errorf("url must include a host")
	}
	return trimmedURL, nil
}

// compactLinks turns the Reader link summary into a deterministic, bounded list
// so a link-heavy page cannot dominate the tool result.
func compactLinks(readerLinks map[string]string) []pageLink {
	if len(readerLinks) == 0 {
		return nil
	}
	compactedLinks := make([]pageLink, 0, len(readerLinks))
	for linkText, linkURL := range readerLinks {
		trimmedURL := strings.TrimSpace(linkURL)
		if trimmedURL == "" {
			continue
		}
		trimmedText := strings.TrimSpace(linkText)
		if len(trimmedText) > maximumLinkTextSize {
			trimmedText = truncateOnUTF8Boundary(trimmedText, maximumLinkTextSize)
		}
		compactedLinks = append(compactedLinks, pageLink{Text: trimmedText, URL: trimmedURL})
	}
	sort.Slice(compactedLinks, func(firstIndex int, secondIndex int) bool {
		if compactedLinks[firstIndex].URL == compactedLinks[secondIndex].URL {
			return compactedLinks[firstIndex].Text < compactedLinks[secondIndex].Text
		}
		return compactedLinks[firstIndex].URL < compactedLinks[secondIndex].URL
	})
	if len(compactedLinks) > maximumLinkCount {
		compactedLinks = compactedLinks[:maximumLinkCount]
	}
	return compactedLinks
}

// truncateOnUTF8Boundary shortens text to at most maximumSize bytes without
// splitting a multi-byte character, and marks the cut with an ellipsis.
func truncateOnUTF8Boundary(text string, maximumSize int) string {
	if len(text) <= maximumSize {
		return text
	}
	truncatedText := text[:maximumSize]
	for len(truncatedText) > 0 && !isUTF8Boundary(text, len(truncatedText)) {
		truncatedText = truncatedText[:len(truncatedText)-1]
	}
	return strings.TrimSpace(truncatedText) + "…"
}

// isUTF8Boundary reports whether an index falls between two complete UTF-8
// runes so truncation never splits a multi-byte character.
func isUTF8Boundary(text string, index int) bool {
	if index <= 0 || index >= len(text) {
		return true
	}
	return text[index]&0xC0 != 0x80
}
