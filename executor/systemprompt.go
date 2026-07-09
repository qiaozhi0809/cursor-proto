package executor

import "fmt"

// spliceSystemPrompt embeds a caller-provided system prompt at the top of the
// user turn using a well-known instruction wrapper.
//
// Cursor's backend refuses AgentRunRequest.CustomSystemPrompt with
// `unknown option '--system-prompt'`, so we route the caller's prompt through
// the user message. The wrapper below tells the model to treat the enclosed
// text as system-level instructions and preferences the wrapped content over
// any conflicting behaviour the Cursor internal system prompt may impose.
func spliceSystemPrompt(systemPrompt, userText string) string {
	if systemPrompt == "" {
		return userText
	}
	return fmt.Sprintf(
		"<system_instructions>\n%s\n</system_instructions>\n\n%s",
		systemPrompt,
		userText,
	)
}
