package executor

import (
	"os"

	cursorpb "github.com/router-for-me/cursor-proto/gen/cursor"
)

// buildAgentRunRequest constructs the top-level AgentRunRequest for a chat.
// See docs/schema-3.10.md for the field layout.
func (c *Client) buildAgentRunRequest(req *ChatRequest, messageID string) (*cursorpb.AgentV1_AgentRunRequest, error) {
	var env *cursorpb.AgentV1_RequestContextEnv
	if !req.PureMode {
		workspace := req.WorkspacePath
		if workspace == "" {
			if wd, err := os.Getwd(); err == nil {
				workspace = wd
			}
		}
		env = &cursorpb.AgentV1_RequestContextEnv{
			OsVersion:      osVersion(),
			WorkspacePaths: []string{workspace},
			Shell:          defaultShell(),
			TimeZone:       timezone(),
			ProjectFolder:  workspace,
		}
	} else {
		env = &cursorpb.AgentV1_RequestContextEnv{
			OsVersion: osVersion(),
			TimeZone:  timezone(),
		}
	}
	reqCtx := &cursorpb.AgentV1_RequestContext{Env: env}

	userMsg := &cursorpb.AgentV1_UserMessage{
		Text:      req.UserMessage,
		MessageId: messageID,
		Mode:      cursorpb.AgentV1_AgentMode(req.Mode),
	}
	umAction := &cursorpb.AgentV1_UserMessageAction{
		UserMessage:    userMsg,
		RequestContext: reqCtx,
	}
	action := &cursorpb.AgentV1_ConversationAction{
		Action: &cursorpb.AgentV1_ConversationAction_UserMessageAction{
			UserMessageAction: umAction,
		},
	}
	model := &cursorpb.AgentV1_ModelDetails{ModelId: req.Model}

	trueVal := true
	mode := cursorpb.AgentV1_AgentMode(req.Mode)
	convState := &cursorpb.AgentV1_ConversationStateStructure{Mode: &mode}

	arr := &cursorpb.AgentV1_AgentRunRequest{
		ConversationState:          convState,
		Action:                     action,
		ModelDetails:               model,
		ConversationId:             ptr(req.ConversationID),
		ClientSupportsInlineImages: &trueVal,
		ClientSupportsSendToUser:   &trueVal,
	}
	if req.Harness != "" {
		arr.Harness = &req.Harness
	} else if !req.PureMode {
		arr.Harness = ptr("cursor-ide")
	}
	// NOTE: We *do not* forward the request's SystemPrompt into
	// CustomSystemPrompt — Cursor's backend rejects that field with
	// `unknown option '--system-prompt'` regardless of harness. Instead,
	// callers should splice the system prompt into UserMessage themselves
	// (RunChat does this automatically).
	return arr, nil
}

func ptr[T any](v T) *T { return &v }

func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}
