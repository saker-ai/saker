package clikit

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/google/uuid"
)

// IMSidePromptWrapper is the system prompt for /im side questions.
// In TUI mode, /im only manages channel credentials — it does NOT start/stop the bridge.
const IMSidePromptWrapper = `<system-reminder>You are a side agent for IM channel configuration. The main agent is NOT interrupted.

You have access to the im_config tool. Use it to manage IM channel credentials stored in ~/.saker/channels.json.

AVAILABLE ACTIONS:
- save: Store credentials for a platform
- list: Show all configured channels
- delete: Remove a channel configuration

WORKFLOW:
1. If the user wants to configure a platform, ask for the required credential fields:
   - Telegram: token (from @BotFather)
   - Feishu: app_id + app_secret (from open.feishu.cn)
   - Discord: token (from discord.com/developers)
   - Slack: bot_token + app_token (from api.slack.com)
   - DingTalk: client_id + client_secret (from open-dev.dingtalk.com)
   - WeCom: corp_id + corp_secret + agent_id
   - QQ: ws_url (optional, default ws://127.0.0.1:3001)
   - QQBot: app_id + app_secret (from q.qq.com)
   - LINE: channel_secret + channel_token (from developers.line.biz)
   - WeChat/Weixin: token (ilink bot Bearer token)
2. Once credentials are provided, call im_config with action "save".
3. For "list" or "delete" requests, call im_config directly.
4. After saving, remind the user to start the bridge with: saker --gateway <platform>

IMPORTANT: This tool only saves/manages credentials. To actually start the IM bridge, the user must run 'saker --gateway <platform>' from the command line.

Supported platforms: telegram, feishu, discord, slack, dingtalk, wecom, qq, qqbot, line, weixin

Keep your response concise — one or two sentences plus the tool call.</system-reminder>

`

// RunIMSideQuestion runs an /im side question in an independent session.
// The im_config tool is available through the runtime's tool registry.
func RunIMSideQuestion(ctx context.Context, out, errOut io.Writer, eng StreamEngine, instruction string) error {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}

	tempSessionID := "im-" + uuid.NewString()
	wrappedPrompt := IMSidePromptWrapper + instruction

	ch, err := eng.RunStream(ctx, tempSessionID, wrappedPrompt)
	if err != nil {
		return fmt.Errorf("im: %w", err)
	}

	fmt.Fprintf(out, "\n/im %s\n", instruction)
	fmt.Fprintln(out, strings.Repeat("─", 40))

	for evt := range ch {
		switch evt.Type {
		case api.EventContentBlockDelta:
			if evt.Delta != nil && evt.Delta.Type == "text_delta" {
				fmt.Fprint(out, evt.Delta.Text)
			}
		case api.EventToolExecutionStart:
			fmt.Fprintf(out, "\n[tool: %s]\n", evt.Name)
		case api.EventError:
			if evt.Output != nil {
				fmt.Fprintf(errOut, "im error: %v\n", evt.Output)
			}
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, strings.Repeat("─", 40))
	return nil
}
