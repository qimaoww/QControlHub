package notify

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"
)

// MessageEntity uses Telegram's UTF-16 offsets. Templates are parsed before
// interpolation, so resource names and diagnostics can never create markup.
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url,omitempty"`
}

type Rendered struct {
	Title    string          `json:"title"`
	Body     string          `json:"body"`
	Entities []MessageEntity `json:"entities,omitempty"`
}

// A bounded, non-nested Markdown subset; unmatched/unsupported syntax stays
// literal. HTML is always text. No parser performs I/O or evaluates expressions.
var markdownToken = regexp.MustCompile("(?s)\\\\[\\\\*~`\\[\\]]|```(.*?)```|`([^`\\n]+)`|\\*\\*([^*]+)\\*\\*|\\*([^*\\n]+)\\*|~~([^~\\n]+)~~|\\[([^\\]\\n]+)\\]\\((https?://[^\\s)]+)\\)")
var markdownLanguage = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,30}$`)

func utf16Length(s string) int { return len(utf16.Encode([]rune(s))) }

func markdownText(source string, expand func(string) string) (string, []MessageEntity) {
	var text strings.Builder
	entities := []MessageEntity{}
	last, offset := 0, 0
	appendText := func(s string) {
		text.WriteString(s)
		offset += utf16Length(s)
	}
	for _, match := range markdownToken.FindAllStringSubmatchIndex(source, -1) {
		appendText(expand(source[last:match[0]]))
		raw := source[match[0]:match[1]]
		last = match[1]
		kind, value, target := "", "", ""
		for group, entity := range []string{"pre", "code", "bold", "italic", "strikethrough", "text_link"} {
			i := (group + 1) * 2
			if match[i] >= 0 {
				kind, value = entity, source[match[i]:match[i+1]]
				break
			}
		}
		if kind == "" {
			appendText(expand(raw[1:]))
			continue
		}
		if kind == "text_link" {
			target = expand(source[match[14]:match[15]])
			u, err := url.Parse(target)
			if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(target, "\r\n") {
				appendText(expand(raw))
				continue
			}
		}
		if kind == "pre" {
			if i := strings.IndexByte(value, '\n'); i >= 0 && (i == 0 || markdownLanguage.MatchString(value[:i])) {
				value = value[i+1:]
			}
			value = strings.TrimSuffix(value, "\n")
		}
		value = expand(value)
		length := utf16Length(value)
		if length > 0 && len(entities) < 99 {
			entities = append(entities, MessageEntity{Type: kind, Offset: offset, Length: length, URL: target})
		}
		appendText(value)
	}
	appendText(expand(source[last:]))
	return text.String(), entities
}

func clippedEntities(entities []MessageEntity, text string) []MessageEntity {
	// An ellipsis from Clip is not part of the original formatting span.
	end := utf16Length(strings.TrimSuffix(text, "…"))
	out := []MessageEntity{}
	for _, e := range entities {
		if e.Offset < 0 || e.Offset >= end || e.Length <= 0 {
			continue
		}
		e.Length = min(e.Length, end-e.Offset)
		out = append(out, e)
	}
	return out
}
