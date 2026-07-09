package executor

import (
	cursorpb "github.com/router-for-me/cursor-proto/gen/cursor"
)

// ListModels calls AiService/AvailableModels and returns the model list.
func (c *Client) ListModels() (*cursorpb.AiserverV1_AvailableModelsResponse, error) {
	req := &cursorpb.AiserverV1_AvailableModelsRequest{}
	var resp cursorpb.AiserverV1_AvailableModelsResponse
	if err := c.UnaryCall("aiserver.v1.AiService", "AvailableModels", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDefaultModel calls AiService/GetDefaultModel and returns the raw response.
func (c *Client) GetDefaultModel() (*cursorpb.AiserverV1_GetDefaultModelResponse, error) {
	req := &cursorpb.AiserverV1_GetDefaultModelRequest{}
	var resp cursorpb.AiserverV1_GetDefaultModelResponse
	if err := c.UnaryCall("aiserver.v1.AiService", "GetDefaultModel", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
