# Cursor 3.10.20 Protobuf Schema (逆向)

- **Version**: 3.10.20
- **Commit**: `23b9fb205fe595ea2be29da7214e19762d037fc0`
- **Build date**: 2026-07-07
- **Source**: `/Applications/Cursor.app/Contents/Resources/app/out/vs/workbench/workbench.desktop.main.js` (39 MB)
- **Extracted**: 5064 messages, 395 enums

## 命名空间

| Namespace | Messages |
|---|---|
| `aiserver.v1` | 4132 |
| `agent.v1` | 894 |
| `anyrun.v1` | 35 |
| `internapi.v1` | 3 |

## 核心消息定义

### `agent.v1.AgentRunRequest` (23 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | message | `agent.v1.ConversationStateStructure` | `conversation_state` |  |
| 2 | message | `agent.v1.ConversationAction` | `action` |  |
| 3 | message | `agent.v1.ModelDetails` | `model_details` |  |
| 9 | message | `agent.v1.RequestedModel` | `requested_model` |  |
| 4 | message | `agent.v1.McpTools` | `mcp_tools` |  |
| 5 | scalar | `string` | `conversation_id` |  |
| 6 | message | `agent.v1.McpFileSystemOptions` | `mcp_file_system_options` |  |
| 7 | message | `agent.v1.SkillOptions` | `skill_options` |  |
| 8 | scalar | `string` | `custom_system_prompt` |  |
| 10 | scalar | `bool` | `suggest_next_prompt` |  |
| 11 | scalar | `string` | `subagent_type_name` |  |
| 12 | scalar | `bool` | `exclude_workspace_context` |  |
| 13 | scalar | `string` | `harness` |  |
| 14 | message | `agent.v1.RequestedModel` | `selected_subagent_models` |  |
| 15 | message | `agent.v1.ModelDetails` | `selected_subagent_model_details` |  |
| 16 | scalar | `string` | `conversation_group_id` |  |
| 17 | message | `agent.v1.PreFetchedBlob` | `pre_fetched_blobs` |  |
| 18 | scalar | `string` | `dev_raw_model_slug` |  |
| 19 | scalar | `bool` | `client_supports_inline_images` |  |
| 20 | message | `agent.v1.SubagentModelOverride` | `subagent_model_overrides` |  |
| 21 | scalar | `bool` | `can_create_cloud_subagents` |  |
| 22 | scalar | `bool` | `suppress_subagent_progress_update_tool` |  |
| 23 | scalar | `bool` | `client_supports_send_to_user` |  |

### `agent.v1.UserMessageAction` (6 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | message | `agent.v1.UserMessage` | `user_message` |  |
| 2 | message | `agent.v1.RequestContext` | `request_context` |  |
| 3 | scalar | `bool` | `send_to_interaction_listener` |  |
| 4 | message | `agent.v1.UserMessage` | `prepend_user_messages` |  |
| 6 | message | `agent.v1.InterruptedPendingToolCallResolutions` | `interrupted_pending_tool_call_resolutions` |  |
| 7 | message | `agent.v1.ConversationHistory` | `conversation_history` |  |

### `agent.v1.RequestContext` (44 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 2 | message | `agent.v1.CursorRule` | `rules` |  |
| 4 | message | `agent.v1.RequestContextEnv` | `env` |  |
| 6 | message | `agent.v1.RepositoryIndexingInfo` | `repository_info` |  |
| 7 | message | `agent.v1.McpToolDefinition` | `tools` |  |
| 8 | scalar | `string` | `conversation_notes_listing` |  |
| 9 | scalar | `string` | `shared_notes_listing` |  |
| 11 | message | `agent.v1.GitRepoInfo` | `git_repos` |  |
| 13 | message | `agent.v1.LsDirectoryTreeNode` | `project_layouts` |  |
| 14 | message | `agent.v1.McpInstructions` | `mcp_instructions` |  |
| 15 | message | `agent.v1.DebugModeConfig` | `debug_mode_config` |  |
| 16 | scalar | `string` | `cloud_rule` |  |
| 17 | scalar | `bool` | `web_search_enabled` |  |
| 18 | message | `agent.v1.SkillOptions` | `skill_options` |  |
| 19 | scalar | `bool` | `repository_info_should_query_prod` |  |
| 20 | map | `map<string,string>` | `file_contents` |  |
| 21 | scalar | `string` | `user_intent_summary` |  |
| 22 | message | `agent.v1.CustomSubagent` | `custom_subagents` |  |
| 23 | message | `agent.v1.McpFileSystemOptions` | `mcp_file_system_options` |  |
| 24 | scalar | `bool` | `web_fetch_enabled` |  |
| 25 | scalar | `string` | `hooks_additional_context` |  |
| 26 | scalar | `string` | `commit_attribution_message` |  |
| 27 | scalar | `string` | `pr_attribution_message` |  |
| 28 | message | `agent.v1.HooksConfigInfo` | `hooks_config` |  |
| 29 | message | `agent.v1.AgentSkill` | `agent_skills` |  |
| 30 | message | `agent.v1.PrecomputedHumanChange` | `precomputed_human_changes` |  |
| 31 | message | `agent.v1.RecentlyAddedPlugin` | `recently_added_plugin` |  |
| 32 | scalar | `bool` | `supports_mcp_auth` |  |
| 33 | scalar | `bool` | `git_repo_info_complete` |  |
| 34 | message | `agent.v1.McpMetaToolOptions` | `mcp_meta_tool_options` |  |
| 35 | scalar | `bool` | `read_lints_enabled` |  |
| 36 | scalar | `bool` | `mcp_info_complete` |  |
| 37 | message | `agent.v1.CursorRule` | `non_file_rules` |  |
| 38 | message | `agent.v1.MatchedInstalledPlugin` | `matched_installed_plugin` |  |
| 39 | scalar | `bool` | `rules_info_complete` |  |
| 40 | scalar | `bool` | `env_info_complete` |  |
| 41 | scalar | `bool` | `repository_info_complete` |  |
| 42 | scalar | `bool` | `custom_subagents_info_complete` |  |
| 43 | scalar | `bool` | `agent_skills_info_complete` |  |
| 44 | scalar | `bool` | `mcp_file_system_info_complete` |  |
| 45 | scalar | `bool` | `git_status_info_complete` |  |
| 46 | message | `agent.v1.PermissionsAutoRunInstructions` | `user_permissions_auto_run` |  |
| 47 | message | `agent.v1.PermissionsAutoRunInstructions` | `project_permissions_auto_run` |  |
| 48 | message | `agent.v1.PermissionsAutoRunInstructions` | `admin_permissions_auto_run` |  |
| 49 | scalar | `string` | `disabled_team_rules` |  |

### `agent.v1.RequestContextEnv` (21 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `string` | `os_version` |  |
| 2 | scalar | `string` | `workspace_paths` |  |
| 3 | scalar | `string` | `shell` |  |
| 5 | scalar | `bool` | `sandbox_enabled` |  |
| 7 | scalar | `string` | `terminals_folder` |  |
| 8 | scalar | `string` | `agent_shared_notes_folder` |  |
| 9 | scalar | `string` | `agent_conversation_notes_folder` |  |
| 10 | scalar | `string` | `time_zone` |  |
| 11 | scalar | `string` | `project_folder` |  |
| 12 | scalar | `string` | `agent_transcripts_folder` |  |
| 13 | scalar | `string` | `artifacts_folder` |  |
| 14 | scalar | `bool` | `sandbox_supported` |  |
| 16 | scalar | `bool` | `sandbox_network_has_defaults` |  |
| 17 | scalar | `string` | `sandbox_network_explicit_allowlist` |  |
| 18 | scalar | `bool` | `secret_redaction_enabled` |  |
| 19 | scalar | `bool` | `computer_use_supported` |  |
| 20 | scalar | `bool` | `is_working_dir_home_dir` |  |
| 21 | scalar | `string` | `process_working_directory` |  |
| 22 | scalar | `bool` | `smart_mode_classifier_auto_mode_enabled` |  |
| 23 | scalar | `string` | `dev_force_next_smart_mode_classifier_block_token` |  |
| 24 | scalar | `string` | `dev_delay_next_smart_mode_classifier_token` |  |

### `agent.v1.McpTools` (1 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | message | `agent.v1.McpToolDefinition` | `mcp_tools` |  |

### `agent.v1.McpToolDefinition` (5 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `string` | `name` |  |
| 4 | scalar | `string` | `provider_identifier` |  |
| 5 | scalar | `string` | `tool_name` |  |
| 2 | scalar | `string` | `description` |  |
| 3 | message | `lY` | `input_schema` |  |

### `agent.v1.McpFileSystemOptions` (3 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `bool` | `enabled` |  |
| 2 | scalar | `string` | `workspace_project_dir` |  |
| 3 | message | `agent.v1.McpDescriptor` | `mcp_descriptors` |  |

### `agent.v1.ModelDetails` (10 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `string` | `model_id` |  |
| 3 | scalar | `string` | `display_model_id` |  |
| 4 | scalar | `string` | `display_name` |  |
| 5 | scalar | `string` | `display_name_short` |  |
| 6 | scalar | `string` | `aliases` |  |
| 2 | message | `agent.v1.ThinkingDetails` | `thinking_details` |  |
| 7 | scalar | `bool` | `max_mode` |  |
| 8 | message | `agent.v1.ApiKeyCredentials` | `api_key_credentials` | oneof:credentials |
| 9 | message | `agent.v1.AzureCredentials` | `azure_credentials` | oneof:credentials |
| 10 | message | `agent.v1.BedrockCredentials` | `bedrock_credentials` | oneof:credentials |

### `agent.v1.RequestedModel` (8 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `string` | `model_id` |  |
| 2 | scalar | `bool` | `max_mode` |  |
| 3 | message | `agent.v1.RequestedModel.ModelParameterValue` | `parameters` |  |
| 4 | message | `agent.v1.ApiKeyCredentials` | `api_key_credentials` | oneof:credentials |
| 5 | message | `agent.v1.AzureCredentials` | `azure_credentials` | oneof:credentials |
| 6 | message | `agent.v1.BedrockCredentials` | `bedrock_credentials` | oneof:credentials |
| 7 | scalar | `bool` | `built_in_model` |  |
| 8 | scalar | `bool` | `is_variant_string_representation` |  |

### `agent.v1.ConversationStateStructure` (29 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `bytes` | `root_prompt_messages_json` |  |
| 8 | scalar | `bytes` | `turns` |  |
| 3 | scalar | `bytes` | `todos` |  |
| 4 | scalar | `string` | `pending_tool_calls` |  |
| 5 | message | `agent.v1.ConversationTokenDetails` | `token_details` |  |
| 6 | scalar | `bytes` | `summary` |  |
| 7 | scalar | `bytes` | `plan` |  |
| 9 | scalar | `string` | `previous_workspace_uris` |  |
| 10 | enum | `agent.v1.AgentMode` | `mode` |  |
| 11 | scalar | `bytes` | `summary_archive` |  |
| 12 | map | `map<string,bytes>` | `file_states` |  |
| 15 | map | `map<string,agent.v1.FileStateStructure>` | `file_states_v2` |  |
| 13 | scalar | `bytes` | `summary_archives` |  |
| 14 | message | `agent.v1.StepTiming` | `turn_timings` |  |
| 16 | map | `map<string,agent.v1.SubagentPersistedState>` | `subagent_states` |  |
| 17 | scalar | `uint32` | `self_summary_count` |  |
| 18 | scalar | `string` | `read_paths` |  |
| 19 | scalar | `string` | `active_branch_name` |  |
| 20 | map | `map<string,agent.v1.PlanRegistryEntry>` | `plans` |  |
| 21 | message | `agent.v1.TrackedGitRepo` | `tracked_git_repo_branches` |  |
| 22 | scalar | `string` | `agent_type` |  |
| 23 | message | `agent.v1.CommunicateUpdateHistoryEntry` | `communicate_update_history` |  |
| 24 | map | `map<string,string>` | `subagent_threads` |  |
| 25 | scalar | `string` | `communicate_update_final_summary` |  |
| 28 | scalar | `string` | `communicate_update_completed_subtitle` |  |
| 29 | map | `map<string,agent.v1.CommunicateUpdateTurnState>` | `communicate_update_states_by_parent_tool_call_id` |  |
| 30 | map | `map<string,agent.v1.SubagentRunState>` | `subagent_runs_by_parent_tool_call_id` |  |
| 26 | scalar | `uint64` | `conversation_started_timestamp_ms` |  |
| 27 | scalar | `string` | `conversation_started_time_zone` |  |

### `agent.v1.ConversationAction` (14 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | message | `agent.v1.UserMessageAction` | `user_message_action` | oneof:action |
| 2 | message | `agent.v1.ResumeAction` | `resume_action` | oneof:action |
| 3 | message | `agent.v1.CancelAction` | `cancel_action` | oneof:action |
| 4 | message | `agent.v1.SummarizeAction` | `summarize_action` | oneof:action |
| 5 | message | `agent.v1.ShellCommandAction` | `shell_command_action` | oneof:action |
| 6 | message | `agent.v1.StartPlanAction` | `start_plan_action` | oneof:action |
| 7 | message | `agent.v1.ExecutePlanAction` | `execute_plan_action` | oneof:action |
| 8 | message | `agent.v1.AsyncAskQuestionCompletionAction` | `async_ask_question_completion_action` | oneof:action |
| 10 | message | `agent.v1.CancelSubagentAction` | `cancel_subagent_action` | oneof:action |
| 12 | message | `agent.v1.BackgroundTaskCompletionAction` | `background_task_completion_action` | oneof:action |
| 13 | message | `agent.v1.BackgroundShellAction` | `background_shell_action` | oneof:action |
| 14 | message | `agent.v1.BackgroundSubagentAction` | `background_subagent_action` | oneof:action |
| 11 | scalar | `string` | `triggering_auth_id` |  |
| 15 | message | `agent.v1.TriggeringUserInfo` | `triggering_user_info` |  |

### `agent.v1.AgentServerMessage` (6 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | message | `agent.v1.InteractionUpdate` | `interaction_update` | oneof:message |
| 2 | message | `agent.v1.ExecServerMessage` | `exec_server_message` | oneof:message |
| 5 | message | `$In` | `exec_server_control_message` | oneof:message |
| 3 | message | `agent.v1.ConversationStateStructure` | `conversation_checkpoint_update` | oneof:message |
| 4 | message | `agent.v1.KvServerMessage` | `kv_server_message` | oneof:message |
| 7 | message | `agent.v1.InteractionQuery` | `interaction_query` | oneof:message |

### `agent.v1.ExecServerMessage` (40 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `uint32` | `id` |  |
| 15 | scalar | `string` | `exec_id` |  |
| 2 | message | `agent.v1.ShellArgs` | `shell_args` | oneof:message |
| 3 | message | `agent.v1.WriteArgs` | `write_args` | oneof:message |
| 4 | message | `agent.v1.DeleteArgs` | `delete_args` | oneof:message |
| 5 | message | `agent.v1.GrepArgs` | `grep_args` | oneof:message |
| 7 | message | `agent.v1.ReadArgs` | `read_args` | oneof:message |
| 29 | message | `agent.v1.ReadArgs` | `redacted_read_args` | oneof:message |
| 8 | message | `agent.v1.LsArgs` | `ls_args` | oneof:message |
| 9 | message | `agent.v1.DiagnosticsArgs` | `diagnostics_args` | oneof:message |
| 10 | message | `agent.v1.RequestContextArgs` | `request_context_args` | oneof:message |
| 11 | message | `agent.v1.McpArgs` | `mcp_args` | oneof:message |
| 14 | message | `agent.v1.ShellArgs` | `shell_stream_args` | oneof:message |
| 16 | message | `agent.v1.BackgroundShellSpawnArgs` | `background_shell_spawn_args` | oneof:message |
| 17 | message | `agent.v1.ListMcpResourcesExecArgs` | `list_mcp_resources_exec_args` | oneof:message |
| 18 | message | `agent.v1.ReadMcpResourceExecArgs` | `read_mcp_resource_exec_args` | oneof:message |
| 36 | message | `agent.v1.McpStateExecArgs` | `mcp_state_exec_args` | oneof:message |
| 20 | message | `agent.v1.FetchArgs` | `fetch_args` | oneof:message |
| 21 | message | `agent.v1.RecordScreenArgs` | `record_screen_args` | oneof:message |
| 22 | message | `agent.v1.ComputerUseArgs` | `computer_use_args` | oneof:message |
| 23 | message | `agent.v1.WriteShellStdinArgs` | `write_shell_stdin_args` | oneof:message |
| 27 | message | `agent.v1.ExecuteHookArgs` | `execute_hook_args` | oneof:message |
| 28 | message | `agent.v1.SubagentArgs` | `subagent_args` | oneof:message |
| 30 | message | `agent.v1.ForceBackgroundShellArgs` | `force_background_shell_args` | oneof:message |
| 31 | message | `agent.v1.ForceBackgroundSubagentArgs` | `force_background_subagent_args` | oneof:message |
| 37 | message | `agent.v1.SubagentAwaitArgs` | `subagent_await_args` | oneof:message |
| 38 | message | `agent.v1.SmartModeClassifierArgs` | `smart_mode_classifier_args` | oneof:message |
| 40 | message | `agent.v1.CanvasDiagnosticsArgs` | `canvas_diagnostics_args` | oneof:message |
| 41 | message | `agent.v1.ShellAllowlistPrecheckArgs` | `shell_allowlist_precheck_args` | oneof:message |
| 42 | message | `agent.v1.McpAllowlistPrecheckArgs` | `mcp_allowlist_precheck_args` | oneof:message |
| 43 | message | `agent.v1.WebFetchAllowlistPrecheckArgs` | `web_fetch_allowlist_precheck_args` | oneof:message |
| 44 | message | `aiserver.v1.GetDiffRequest` | `git_diff_request` | oneof:message |
| 45 | message | `agent.v1.PiReadExecArgs` | `pi_read_args` | oneof:message |
| 46 | message | `agent.v1.PiBashExecArgs` | `pi_bash_args` | oneof:message |
| 47 | message | `agent.v1.PiEditExecArgs` | `pi_edit_args` | oneof:message |
| 48 | message | `agent.v1.PiWriteExecArgs` | `pi_write_args` | oneof:message |
| 49 | message | `agent.v1.PiGrepExecArgs` | `pi_grep_args` | oneof:message |
| 50 | message | `agent.v1.PiFindExecArgs` | `pi_find_args` | oneof:message |
| 51 | message | `agent.v1.PiLsExecArgs` | `pi_ls_args` | oneof:message |
| 19 | message | `agent.v1.SpanContext` | `span_context` |  |

### `agent.v1.ExecClientMessage` (41 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `uint32` | `id` |  |
| 15 | scalar | `string` | `exec_id` |  |
| 39 | scalar | `int32` | `local_execution_time_ms` |  |
| 45 | message | `agent.v1.HookAdditionalContext` | `hook_additional_contexts` |  |
| 2 | message | `agent.v1.ShellResult` | `shell_result` | oneof:message |
| 3 | message | `agent.v1.WriteResult` | `write_result` | oneof:message |
| 4 | message | `agent.v1.DeleteResult` | `delete_result` | oneof:message |
| 5 | message | `agent.v1.GrepResult` | `grep_result` | oneof:message |
| 7 | message | `agent.v1.ReadResult` | `read_result` | oneof:message |
| 29 | message | `agent.v1.ReadResult` | `redacted_read_result` | oneof:message |
| 8 | message | `agent.v1.LsResult` | `ls_result` | oneof:message |
| 9 | message | `agent.v1.DiagnosticsResult` | `diagnostics_result` | oneof:message |
| 10 | message | `agent.v1.RequestContextResult` | `request_context_result` | oneof:message |
| 11 | message | `agent.v1.McpResult` | `mcp_result` | oneof:message |
| 14 | message | `agent.v1.ShellStream` | `shell_stream` | oneof:message |
| 16 | message | `agent.v1.BackgroundShellSpawnResult` | `background_shell_spawn_result` | oneof:message |
| 17 | message | `agent.v1.ListMcpResourcesExecResult` | `list_mcp_resources_exec_result` | oneof:message |
| 18 | message | `agent.v1.ReadMcpResourceExecResult` | `read_mcp_resource_exec_result` | oneof:message |
| 36 | message | `agent.v1.McpStateExecResult` | `mcp_state_exec_result` | oneof:message |
| 20 | message | `agent.v1.FetchResult` | `fetch_result` | oneof:message |
| 21 | message | `agent.v1.RecordScreenResult` | `record_screen_result` | oneof:message |
| 22 | message | `agent.v1.ComputerUseResult` | `computer_use_result` | oneof:message |
| 23 | message | `agent.v1.WriteShellStdinResult` | `write_shell_stdin_result` | oneof:message |
| 27 | message | `agent.v1.ExecuteHookResult` | `execute_hook_result` | oneof:message |
| 28 | message | `agent.v1.SubagentResult` | `subagent_result` | oneof:message |
| 30 | message | `agent.v1.ForceBackgroundShellResult` | `force_background_shell_result` | oneof:message |
| 31 | message | `agent.v1.ForceBackgroundSubagentResult` | `force_background_subagent_result` | oneof:message |
| 37 | message | `agent.v1.SubagentAwaitResult` | `subagent_await_result` | oneof:message |
| 38 | message | `agent.v1.SmartModeClassifierResult` | `smart_mode_classifier_result` | oneof:message |
| 40 | message | `agent.v1.CanvasDiagnosticsResult` | `canvas_diagnostics_result` | oneof:message |
| 41 | message | `agent.v1.ShellAllowlistPrecheckResult` | `shell_allowlist_precheck_result` | oneof:message |
| 42 | message | `agent.v1.McpAllowlistPrecheckResult` | `mcp_allowlist_precheck_result` | oneof:message |
| 43 | message | `agent.v1.WebFetchAllowlistPrecheckResult` | `web_fetch_allowlist_precheck_result` | oneof:message |
| 44 | message | `aiserver.v1.GetDiffResponse` | `git_diff_response` | oneof:message |
| 46 | message | `agent.v1.PiReadExecResult` | `pi_read_result` | oneof:message |
| 47 | message | `agent.v1.PiBashExecResult` | `pi_bash_result` | oneof:message |
| 48 | message | `agent.v1.PiEditExecResult` | `pi_edit_result` | oneof:message |
| 49 | message | `agent.v1.PiWriteExecResult` | `pi_write_result` | oneof:message |
| 50 | message | `agent.v1.PiGrepExecResult` | `pi_grep_result` | oneof:message |
| 51 | message | `agent.v1.PiFindExecResult` | `pi_find_result` | oneof:message |
| 52 | message | `agent.v1.PiLsExecResult` | `pi_ls_result` | oneof:message |

### `aiserver.v1.AvailableModelsRequest` (13 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | scalar | `bool` | `is_nightly` |  |
| 2 | scalar | `bool` | `include_long_context_models` |  |
| 3 | scalar | `bool` | `exclude_max_named_models` |  |
| 4 | scalar | `string` | `additional_model_names` |  |
| 5 | scalar | `bool` | `use_model_parameters` |  |
| 6 | scalar | `bool` | `include_hidden_models` |  |
| 7 | scalar | `bool` | `do_not_use_markdown` |  |
| 8 | scalar | `bool` | `variants_will_be_shown_in_exploded_list` |  |
| 9 | scalar | `bool` | `for_automations` |  |
| 10 | enum | `aiserver.v1.AvailableModelsScope` | `scope` |  |
| 11 | scalar | `bool` | `use_react_model_picker` |  |
| 12 | scalar | `bool` | `use_cloud_agent_effort_modes` |  |
| 13 | scalar | `string` | `admin_settings_group_public_id` |  |

### `aiserver.v1.AvailableModelsResponse` (14 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 2 | message | `aiserver.v1.AvailableModelsResponse.AvailableModel` | `models` |  |
| 1 | scalar | `string` | `model_names` |  |
| 4 | message | `aiserver.v1.AvailableModelsResponse.FeatureModelConfig` | `composer_model_config` |  |
| 5 | message | `aiserver.v1.AvailableModelsResponse.FeatureModelConfig` | `cmd_k_model_config` |  |
| 6 | message | `aiserver.v1.AvailableModelsResponse.FeatureModelConfig` | `background_composer_model_config` |  |
| 7 | message | `aiserver.v1.AvailableModelsResponse.FeatureModelConfig` | `plan_execution_model_config` |  |
| 8 | message | `aiserver.v1.AvailableModelsResponse.FeatureModelConfig` | `spec_model_config` |  |
| 9 | message | `aiserver.v1.AvailableModelsResponse.FeatureModelConfig` | `deep_search_model_config` |  |
| 10 | message | `aiserver.v1.AvailableModelsResponse.FeatureModelConfig` | `quick_agent_model_config` |  |
| 16 | map | `map<string,aiserver.v1.AvailableModelsResponse.FeatureModelConfig>` | `subagent_model_configs` |  |
| 11 | scalar | `bool` | `use_model_parameters` |  |
| 12 | scalar | `int32` | `disable_unused_models_after_n_hours` |  |
| 13 | scalar | `int32` | `upgrade_unchanged_models_after_n_hours` |  |
| 15 | message | `aiserver.v1.AvailableModelsResponse.ModelPickerDisplayConfiguration` | `display_configuration` |  |

### `aiserver.v1.ErrorDetails` (3 fields)

| # | Kind | Type | Name | Mods |
|---|---|---|---|---|
| 1 | enum | `aiserver.v1.ErrorDetails.Error` | `error` |  |
| 2 | message | `aiserver.v1.CustomErrorDetails` | `details` |  |
| 3 | scalar | `bool` | `is_expected` |  |
