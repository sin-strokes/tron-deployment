package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerPrompts wires the canonical agent-driven workflows as MCP
// prompt templates. MCP-aware clients (Claude Desktop, Cursor)
// surface these in a slash-command picker; the user picks one, fills
// the arguments, and the LLM gets a structured prompt that already
// understands the trond context.
//
// Why: tools alone require the agent to remember the right sequence
// (validate → plan → apply, diagnose → rollback → verify, etc.).
// Prompts pre-bake the workflow so a less-capable agent or a fresh
// session lands on the right path immediately.
//
// Each prompt's output is a system+user message pair that:
//  1. Tells the LLM which trond tools to call and in what order.
//  2. Declares which arguments map to which tool inputs.
//  3. Includes the canonical AGENTS.md retry semantics (exit codes,
//     structured envelope conventions).
func registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "deploy_fullnode",
		Title:       "Deploy a TRON fullnode",
		Description: "Walk through the canonical deploy workflow for a single fullnode: validate intent → plan → apply --auto-approve --wait → status. Use this when the user wants to bring up a new fullnode end-to-end.",
		Arguments: []*mcp.PromptArgument{
			{Name: "intent_path", Description: "absolute path to the intent.yaml", Required: true},
		},
	}, deployFullnodePrompt)

	s.AddPrompt(&mcp.Prompt{
		Name:        "diagnose_failing_node",
		Title:       "Triage a failing node",
		Description: "Apply trond's structured diagnostic suite to a node that's reporting unhealthy: status → diagnose → logs → suggest fix. Walks through the AGENTS.md exit-code retry tree so the agent's response is reproducible.",
		Arguments: []*mcp.PromptArgument{
			{Name: "node", Description: "managed node name (matches intent.name)", Required: true},
		},
	}, diagnoseFailingNodePrompt)

	s.AddPrompt(&mcp.Prompt{
		Name:        "setup_private_network",
		Title:       "Bootstrap a private TRON network",
		Description: "Stand up a multi-node private network from an intent file. Covers SR_PRIVATE_KEY env, network create, status, and the canonical recovery path when a node fails to come up.",
		Arguments: []*mcp.PromptArgument{
			{Name: "intent_path", Description: "absolute path to the multi-node intent.yaml", Required: true},
		},
	}, setupPrivateNetworkPrompt)

	s.AddPrompt(&mcp.Prompt{
		Name:        "recover_failed_upgrade",
		Title:       "Roll back a failed upgrade",
		Description: "An upgrade left a node unhealthy. Run diagnose, then trigger rollback to the previous_version recorded in state.json, then verify. Idempotent — safe to invoke even if the rollback already happened.",
		Arguments: []*mcp.PromptArgument{
			{Name: "node", Description: "name of the managed node to recover", Required: true},
		},
	}, recoverFailedUpgradePrompt)
}

// promptResult builds a single-message GetPromptResult — every prompt
// in this package returns a user-role message containing the
// pre-filled instructions the LLM should follow.
func promptResult(description, body string) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: description,
		Messages: []*mcp.PromptMessage{{
			Role: "user",
			Content: &mcp.TextContent{
				Text: body,
			},
		}},
	}, nil
}

func deployFullnodePrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	intentPath := req.Params.Arguments["intent_path"]
	if intentPath == "" {
		return nil, fmt.Errorf("intent_path argument is required")
	}
	body := fmt.Sprintf(`Deploy a TRON fullnode end-to-end using trond.

Intent file: %s

Run these tools in order. After each step, parse the JSON response and act on the result_code or error_code before proceeding.

  1. config_validate (path=%[1]q)
     - On valid=true: continue.
     - On error_code=VALIDATION_ERROR: surface the error to the user and STOP. Do not attempt to "fix" the YAML.

  2. plan (path=%[1]q)
     - Show the user the changes[] array and ask for approval.
     - On approval, continue. On rejection, STOP.

  3. apply (path=%[1]q, auto_approve=true, wait=true)
     - On result="created" or "updated": continue.
     - On result="no_change": tell the user nothing changed; STOP (no need to call status).
     - On error_code=HUMAN_REQUIRED: this means the diff was destructive. Re-show the plan from step 2 and ask for explicit approval before re-calling apply.
     - On exit_code=1 with error_code=WAIT_TIMEOUT: the node deployed but didn't become ready in 5 minutes. Call diagnose next.

  4. status (name=<intent.name from step 1>)
     - Read block_height + is_synced. Tell the user the node is up and producing/relaying blocks.

Trond's exit-code contract (AGENTS.md):
  0 = success            1 = general            2 = validation
  3 = target unreachable 4 = preflight fail     10 = HUMAN_REQUIRED

Always parse the structured error envelope: {error_code, exit_code, message, suggestions[]}. The suggestions[0] field is the most likely fix; try it before asking the user.`, intentPath)
	return promptResult("Trond fullnode deploy workflow", body)
}

func diagnoseFailingNodePrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	node := req.Params.Arguments["node"]
	if node == "" {
		return nil, fmt.Errorf("node argument is required")
	}
	body := fmt.Sprintf(`Triage the unhealthy node %[1]q using trond's structured diagnostics.

  1. status (name=%[1]q)
     - Note status, is_synced, block_height, peer_count.
     - If error_code=NODE_NOT_FOUND: the node was never deployed (or was removed). Call list to see what IS managed.

  2. diagnose (name=%[1]q)
     - Returns checks[] with each check's status (pass/warning/fail) and suggestions[].
     - For every check with status=fail, surface its name and suggestions to the user.
     - Apply suggestions[0] if it's a tool call (e.g. "Re-run trond apply" → call apply).

  3. health (name=%[1]q)
     - Lighter HTTP probe of getnowblock. Use to confirm whether the node's HTTP API is responding even if other checks fail.

  4. If diagnose reports sync_progress=fail with is_synced=false:
     - The node is alive but behind. Tell the user the lag (in blocks).
     - Don't auto-restart — sync usually catches up.

  5. If diagnose reports peer_count=fail with peer_count=0 and the node is part of a private network:
     - The shared docker network may be misconfigured. Suggest 'trond network destroy' + 'trond network create'.

Stop after step 4/5 with a structured summary: status, root cause, recommended action. Don't make destructive changes (apply, remove, network destroy) without explicit user confirmation.`, node)
	return promptResult("Trond unhealthy-node triage workflow", body)
}

func setupPrivateNetworkPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	intentPath := req.Params.Arguments["intent_path"]
	if intentPath == "" {
		return nil, fmt.Errorf("intent_path argument is required")
	}
	body := fmt.Sprintf(`Bootstrap a multi-node private TRON network from %s.

Pre-flight check (read-only, no side effects):
  1. config_validate (path=%[1]q) — make sure the intent parses.
  2. Read trond://state — confirm no nodes named "<intent.name>-node*" already exist.

If the user has not set SR_PRIVATE_KEY for the witness, the apply will fail with a placeholder localwitness value. Ask the user to set SR_PRIVATE_KEY in their shell BEFORE you call apply (the env var is captured at process start). Suggested instruction:

  export SR_PRIVATE_KEY=<64-hex-chars>

Deploy:
  3. The CLI command for multi-node networks is 'trond network create --intent ...'. The MCP apply tool deploys a single node and is not the right entry point. Ask the user to run:

       SR_PRIVATE_KEY=<key> trond network create --intent %[1]s -o json

     Then poll trond://state until both <name>-node0 and <name>-node1 appear.

Verify:
  4. status on each node — confirm status=running.
  5. diagnose on each node — confirm peer_count > 0 (nodes can see each other over the shared docker network).

If peer_count=0 on either node, the shared docker network 'trond-<intent.name>' may have failed to create. Suggest the recover-failed-upgrade prompt or a 'trond network destroy + create' cycle.`, intentPath)
	return promptResult("Trond private-network bootstrap workflow", body)
}

func recoverFailedUpgradePrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	node := req.Params.Arguments["node"]
	if node == "" {
		return nil, fmt.Errorf("node argument is required")
	}
	body := fmt.Sprintf(`Recover %[1]q from a failed upgrade. This workflow is idempotent — safe to re-run.

  1. diagnose (name=%[1]q)
     - Capture the structured failure signal for the audit trail. Don't act on it (we're rolling back regardless).

  2. Read trond://state to confirm previous_version is non-empty for %[1]q.
     - If previous_version is empty, rollback won't work. The CLI command to use instead:
         trond apply --intent <previous-intent.yaml> --auto-approve

  3. The rollback action is currently CLI-only. Ask the user to run:

       trond rollback %[1]s -o json

     Tell them the expected output is {status: running, version: <prev>, rolled_back_from: <bad>}.

  4. After they confirm rollback succeeded:
     status (name=%[1]q) — confirm the node is back to status=running with version matching the previous_version recorded before the upgrade.

  5. health (name=%[1]q) — confirm the HTTP API is responding.

Don't proceed past step 4 if status != running — escalate to the user with the diagnose output from step 1.`, node)
	return promptResult("Trond failed-upgrade recovery workflow", body)
}
