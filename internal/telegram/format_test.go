package telegram

import "testing"

func TestFormatTelegramHTMLConvertsCommonMarkdown(t *testing.T) {
	markdown := "## Títol **important**\n\n- **Primer** punt\n- Segon amb *cursiva* i `codi`\n1. [Enllaç](https://example.com/a?x=1&y=2)\n\n---\n\n<text>"
	want := "<b>Títol <b>important</b></b>\n\n• <b>Primer</b> punt\n• Segon amb <i>cursiva</i> i <code>codi</code>\n1. <a href=\"https://example.com/a?x=1&amp;y=2\">Enllaç</a>\n\n────────\n\n&lt;text&gt;"

	if got := formatTelegramHTML(markdown); got != want {
		t.Errorf("formatTelegramHTML() = %q, want %q", got, want)
	}
}

func TestFormatTelegramHTMLEscapesAndKeepsInvalidLinksAsText(t *testing.T) {
	markdown := "[No segur](javascript:alert(1)) & **negreta**"
	want := "[No segur](javascript:alert(1)) &amp; <b>negreta</b>"

	if got := formatTelegramHTML(markdown); got != want {
		t.Errorf("formatTelegramHTML() = %q, want %q", got, want)
	}
}

func TestFormatTelegramHTMLFormatsFencedCode(t *testing.T) {
	markdown := "```go\nif a < b {\n}\n```"
	want := "<pre>if a &lt; b {\n}</pre>"

	if got := formatTelegramHTML(markdown); got != want {
		t.Errorf("formatTelegramHTML() = %q, want %q", got, want)
	}
}
