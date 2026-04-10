package audit

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api-audit/internal/model"
)

const (
	systemTextLimit          = 2048
	conversationExcerptLimit = 4096
	maxConversationItems     = 3
)

var base64SummaryPattern = regexp.MustCompile(`\[base64:\s*~(\d+)bytes\]`)

func BuildPromptAuditLog(envelope model.AuditEnvelope, runtimeConfig model.RuntimeConfig) model.PromptAuditLog {
	root := decodeBody(envelope.Body)
	systemText := truncateString(extractSystemText(root), systemTextLimit)
	conversationExcerpt := truncateString(extractConversationExcerpt(root), conversationExcerptLimit)
	toolSummary := extractToolSummary(root)
	attachmentSummary := extractAttachmentSummary(envelope.Body)
	riskTags := detectRiskTags(envelope, systemText, conversationExcerpt, toolSummary, attachmentSummary, runtimeConfig.KeywordRules)

	return model.PromptAuditLog{
		SourceTimestamp:     envelope.Timestamp,
		RedisMessageID:      envelope.MessageID,
		ClientRequestID:     envelope.ClientRequestID,
		RequestID:           envelope.RequestID,
		UserID:              envelope.UserID,
		APIKeyID:            envelope.APIKeyID,
		GroupID:             envelope.GroupID,
		Endpoint:            envelope.Endpoint,
		Model:               envelope.Model,
		Stream:              envelope.Stream,
		Transport:           envelope.Transport,
		WSTurn:              envelope.WSTurn,
		SampleReason:        envelope.SampleReason,
		BodyBytes:           envelope.BodyBytes,
		BodyTruncated:       envelope.BodyTruncated,
		Body:                envelope.Body,
		SystemText:          systemText,
		ConversationExcerpt: conversationExcerpt,
		ToolSummary:         toolSummary,
		AttachmentSummary:   attachmentSummary,
		RiskTags:            riskTags,
		RawEnvelope:         envelope.RawEnvelope,
	}
}

func decodeBody(body string) any {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil
	}
	return decoded
}

func extractSystemText(root any) string {
	top, ok := root.(map[string]any)
	if !ok {
		return ""
	}

	parts := make([]string, 0, 3)
	if instructions := strings.TrimSpace(asString(top["instructions"])); instructions != "" {
		parts = append(parts, instructions)
	}
	if system := strings.TrimSpace(extractTexts(top["system"])); system != "" {
		parts = append(parts, system)
	}
	for _, item := range gatherConversationObjects(top) {
		if !strings.EqualFold(strings.TrimSpace(asString(item["role"])), "system") {
			continue
		}
		text := strings.TrimSpace(extractTexts(item["content"]))
		if text == "" {
			text = strings.TrimSpace(extractTexts(item["parts"]))
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(uniqueStrings(parts), "\n\n")
}

func extractConversationExcerpt(root any) string {
	top, ok := root.(map[string]any)
	if !ok {
		return ""
	}

	items := gatherConversationObjects(top)
	if len(items) == 0 {
		return ""
	}

	snippets := make([]string, 0, maxConversationItems)
	for i := len(items) - 1; i >= 0 && len(snippets) < maxConversationItems; i-- {
		item := items[i]
		role := strings.TrimSpace(asString(item["role"]))
		if role == "" {
			role = "unknown"
		}
		if strings.EqualFold(role, "system") {
			continue
		}

		text := strings.TrimSpace(extractTexts(item["content"]))
		if text == "" {
			text = strings.TrimSpace(extractTexts(item["parts"]))
		}
		if text == "" {
			text = strings.TrimSpace(extractTexts(item["text"]))
		}
		if text == "" {
			continue
		}
		snippets = append(snippets, fmt.Sprintf("%s: %s", role, truncateString(text, 512)))
	}

	for i, j := 0, len(snippets)-1; i < j; i, j = i+1, j-1 {
		snippets[i], snippets[j] = snippets[j], snippets[i]
	}

	return strings.Join(snippets, "\n")
}

func extractToolSummary(root any) []string {
	top, ok := root.(map[string]any)
	if !ok {
		return nil
	}

	raw, ok := top["tools"]
	if !ok {
		return nil
	}

	items, ok := raw.([]any)
	if !ok {
		return nil
	}

	tools := make([]string, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(asString(obj["name"]))
		if name == "" {
			name = strings.TrimSpace(asString(obj["type"]))
		}
		if fn, ok := obj["function"].(map[string]any); ok && name == "" {
			name = strings.TrimSpace(asString(fn["name"]))
		}
		if name != "" {
			tools = append(tools, name)
		}
	}

	tools = uniqueStrings(tools)
	sort.Strings(tools)
	return tools
}

func extractAttachmentSummary(body string) model.AttachmentSummary {
	summary := model.AttachmentSummary{}
	for _, matches := range base64SummaryPattern.FindAllStringSubmatch(body, -1) {
		summary.Base64Count++
		if len(matches) > 1 {
			summary.ApproxBytes += atoi(matches[1])
		}
	}

	if summary.Base64Count == 0 {
		lower := strings.ToLower(body)
		dataURICount := strings.Count(lower, ";base64,")
		summary.Base64Count = dataURICount
	}

	return summary
}

func detectRiskTags(
	envelope model.AuditEnvelope,
	systemText string,
	conversationExcerpt string,
	toolSummary []string,
	attachmentSummary model.AttachmentSummary,
	keywordRules []string,
) []string {
	tags := make([]string, 0, 8)
	if strings.Contains(envelope.Body, "[REDACTED]") {
		tags = append(tags, model.DefaultCredentialRiskTag)
	}
	if len(toolSummary) > 0 {
		tags = append(tags, model.DefaultToolRiskTag)
	}
	if attachmentSummary.Base64Count > 0 {
		tags = append(tags, model.DefaultAttachmentRiskTag)
	}
	if envelope.BodyTruncated {
		tags = append(tags, model.DefaultLongPromptRiskTag)
	}

	lowerCorpus := strings.ToLower(strings.Join([]string{envelope.Body, systemText, conversationExcerpt}, "\n"))
	for _, keyword := range keywordRules {
		normalized := strings.TrimSpace(strings.ToLower(keyword))
		if normalized == "" {
			continue
		}
		if strings.Contains(lowerCorpus, normalized) {
			tags = append(tags, "keyword:"+normalized)
		}
	}

	tags = uniqueStrings(tags)
	sort.Strings(tags)
	return tags
}

func gatherConversationObjects(top map[string]any) []map[string]any {
	keys := []string{"messages", "input", "contents"}
	out := make([]map[string]any, 0, 8)
	for _, key := range keys {
		items, ok := top[key].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				out = append(out, obj)
			}
		}
	}
	return out
}

func extractTexts(v any) string {
	fragments := make([]string, 0, 4)
	collectTexts(v, &fragments)
	return strings.Join(uniqueStrings(fragments), "\n")
}

func collectTexts(v any, out *[]string) {
	switch value := v.(type) {
	case string:
		text := strings.TrimSpace(value)
		if text != "" {
			*out = append(*out, text)
		}
	case []any:
		for _, item := range value {
			collectTexts(item, out)
		}
	case map[string]any:
		if text := strings.TrimSpace(asString(value["text"])); text != "" {
			*out = append(*out, text)
		}
		if text := strings.TrimSpace(asString(value["input_text"])); text != "" {
			*out = append(*out, text)
		}
		for _, key := range []string{"content", "parts", "items", "system"} {
			if child, ok := value[key]; ok {
				collectTexts(child, out)
			}
		}
	}
}

func asString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	default:
		return ""
	}
}

func truncateString(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return strings.TrimSpace(s[:limit]) + "..."
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func atoi(raw string) int {
	n := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
