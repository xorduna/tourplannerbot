package telegram

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var orderedListItemPattern = regexp.MustCompile(`^(\d+)[.)]\s+(.+)$`)

// formatTelegramHTML converts the common Markdown emitted by the model into
// the small HTML subset supported by Telegram. All text is escaped first so a
// model response cannot introduce raw Telegram HTML.
func formatTelegramHTML(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	formattedLines := make([]string, 0, len(lines))
	inCodeBlock := false
	codeLines := make([]string, 0)

	flushCodeBlock := func() {
		formattedLines = append(formattedLines, "<pre>"+html.EscapeString(strings.Join(codeLines, "\n"))+"</pre>")
		codeLines = codeLines[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCodeBlock {
				flushCodeBlock()
				inCodeBlock = false
			} else {
				inCodeBlock = true
			}
			continue
		}
		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}

		formattedLines = append(formattedLines, formatTelegramLine(line))
	}
	if inCodeBlock {
		flushCodeBlock()
	}

	return strings.TrimSpace(strings.Join(formattedLines, "\n"))
}

func formatTelegramLine(line string) string {
	trimmedLine := strings.TrimSpace(line)
	if trimmedLine == "---" || trimmedLine == "***" || trimmedLine == "___" {
		return "────────"
	}
	if heading, isHeading := markdownHeading(trimmedLine); isHeading {
		return "<b>" + formatTelegramInline(heading) + "</b>"
	}
	if strings.HasPrefix(trimmedLine, "> ") {
		return "│ " + formatTelegramInline(strings.TrimPrefix(trimmedLine, "> "))
	}
	if item, isBullet := markdownBulletItem(line); isBullet {
		return "• " + formatTelegramInline(item)
	}
	if matches := orderedListItemPattern.FindStringSubmatch(trimmedLine); matches != nil {
		return matches[1] + ". " + formatTelegramInline(matches[2])
	}
	return formatTelegramInline(line)
}

func markdownHeading(line string) (string, bool) {
	headingLength := 0
	for headingLength < len(line) && line[headingLength] == '#' {
		headingLength++
	}
	if headingLength == 0 || headingLength > 6 || len(line) == headingLength || line[headingLength] != ' ' {
		return "", false
	}
	return strings.TrimSpace(line[headingLength:]), true
}

func markdownBulletItem(line string) (string, bool) {
	trimmedLeft := strings.TrimLeft(line, " \t")
	if len(trimmedLeft) < 3 || (trimmedLeft[0] != '-' && trimmedLeft[0] != '*' && trimmedLeft[0] != '+') || trimmedLeft[1] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmedLeft[2:]), true
}

func formatTelegramInline(markdown string) string {
	var formatted strings.Builder
	for position := 0; position < len(markdown); {
		if strings.HasPrefix(markdown[position:], "\\") {
			_, characterSize := utf8.DecodeRuneInString(markdown[position+1:])
			if position+1 < len(markdown) && characterSize > 0 {
				formatted.WriteString(html.EscapeString(markdown[position+1 : position+1+characterSize]))
				position += 1 + characterSize
				continue
			}
		}
		if strings.HasPrefix(markdown[position:], "[") {
			if endLabel := strings.Index(markdown[position:], "]("); endLabel > 1 {
				labelEnd := position + endLabel
				if endURL := strings.Index(markdown[labelEnd+2:], ")"); endURL >= 0 {
					urlEnd := labelEnd + 2 + endURL
					if linkURL, valid := telegramURL(markdown[labelEnd+2 : urlEnd]); valid {
						formatted.WriteString("<a href=\"")
						formatted.WriteString(html.EscapeString(linkURL))
						formatted.WriteString("\">")
						formatted.WriteString(formatTelegramInline(markdown[position+1 : labelEnd]))
						formatted.WriteString("</a>")
						position = urlEnd + 1
						continue
					}
				}
			}
		}
		if marker, tag, found := markdownInlineMarker(markdown[position:]); found {
			contentStart := position + len(marker)
			if contentEnd := strings.Index(markdown[contentStart:], marker); contentEnd >= 0 {
				contentEnd += contentStart
				if contentEnd > contentStart {
					formatted.WriteString("<")
					formatted.WriteString(tag)
					formatted.WriteString(">")
					if marker == "`" {
						formatted.WriteString(html.EscapeString(markdown[contentStart:contentEnd]))
					} else {
						formatted.WriteString(formatTelegramInline(markdown[contentStart:contentEnd]))
					}
					formatted.WriteString("</")
					formatted.WriteString(tag)
					formatted.WriteString(">")
					position = contentEnd + len(marker)
					continue
				}
			}
		}

		character, characterSize := utf8.DecodeRuneInString(markdown[position:])
		formatted.WriteString(html.EscapeString(string(character)))
		position += characterSize
	}
	return formatted.String()
}

func markdownInlineMarker(markdown string) (marker string, tag string, found bool) {
	switch {
	case strings.HasPrefix(markdown, "**"):
		return "**", "b", true
	case strings.HasPrefix(markdown, "__"):
		return "__", "u", true
	case strings.HasPrefix(markdown, "~~"):
		return "~~", "s", true
	case strings.HasPrefix(markdown, "`"):
		return "`", "code", true
	case strings.HasPrefix(markdown, "*"):
		return "*", "i", true
	case strings.HasPrefix(markdown, "_"):
		return "_", "i", true
	default:
		return "", "", false
	}
}

func telegramURL(rawURL string) (string, bool) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") || parsedURL.Host == "" {
		return "", false
	}
	return rawURL, true
}
