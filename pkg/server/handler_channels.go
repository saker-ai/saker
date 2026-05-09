package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cinience/saker/pkg/config"
	"github.com/godeps/goim"
)

// platformMeta describes a supported IM platform for the frontend.
type platformMeta struct {
	Name   string          `json:"name"`
	Icon   string          `json:"icon"`
	Fields []platformField `json:"fields"`
}

type platformField struct {
	Key    string `json:"key"`
	Label  string `json:"label,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

var platforms = map[string]platformMeta{
	"telegram": {Name: "Telegram", Icon: "send", Fields: []platformField{{Key: "token", Label: "Bot Token", Secret: true}}},
	"discord":  {Name: "Discord", Icon: "gamepad", Fields: []platformField{{Key: "token", Label: "Bot Token", Secret: true}}},
	"feishu":   {Name: "Feishu", Icon: "feather", Fields: []platformField{{Key: "app_id", Label: "App ID"}, {Key: "app_secret", Label: "App Secret", Secret: true}}},
	"slack":    {Name: "Slack", Icon: "hash", Fields: []platformField{{Key: "bot_token", Label: "Bot Token", Secret: true}, {Key: "app_token", Label: "App Token", Secret: true}}},
	"dingtalk": {Name: "DingTalk", Icon: "bell", Fields: []platformField{{Key: "client_id", Label: "Client ID"}, {Key: "client_secret", Label: "Client Secret", Secret: true}}},
	"wecom":    {Name: "WeCom", Icon: "briefcase", Fields: []platformField{{Key: "corp_id", Label: "Corp ID"}, {Key: "corp_secret", Label: "Corp Secret", Secret: true}, {Key: "agent_id", Label: "Agent ID"}}},
	"qq":       {Name: "QQ", Icon: "message-circle", Fields: []platformField{{Key: "ws_url", Label: "WebSocket URL"}}},
	"qqbot":    {Name: "QQ Bot", Icon: "bot", Fields: []platformField{{Key: "app_id", Label: "App ID"}, {Key: "app_secret", Label: "App Secret", Secret: true}}},
	"line":     {Name: "LINE", Icon: "phone", Fields: []platformField{{Key: "channel_secret", Label: "Channel Secret", Secret: true}, {Key: "channel_token", Label: "Channel Token", Secret: true}}},
	"weixin":   {Name: "WeChat", Icon: "message-square", Fields: []platformField{{Key: "token", Label: "Token", Secret: true}}},
}

// channelsJSONPath returns the path to ~/.saker/channels.json.
func channelsJSONPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".saker", "channels.json")
}

// handleChannelsList returns all configured channels with redacted secrets.
func (h *Handler) handleChannelsList(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	path := channelsJSONPath()
	if path == "" {
		return h.internalError(req.ID, "cannot determine home directory")
	}

	chCfg, err := goim.LoadChannelsJSON(path)
	if err != nil {
		return h.internalError(req.ID, "load channels: "+err.Error())
	}

	// Build response with all platforms (configured or not).
	type channelInfo struct {
		Platform   string            `json:"platform"`
		Name       string            `json:"name"`
		Icon       string            `json:"icon"`
		Enabled    bool              `json:"enabled"`
		Configured bool              `json:"configured"`
		Fields     []platformField   `json:"fields"`
		Values     map[string]string `json:"values"`
		Route      string            `json:"route,omitempty"`
	}

	// Look up routes from settings.
	routeMap := map[string]string{} // channel → persona
	s := h.runtime.Settings()
	if s != nil && s.Personas != nil {
		for _, r := range s.Personas.Routes {
			routeMap[r.Channel] = r.Persona
		}
	}

	var result []channelInfo
	for pid, meta := range platforms {
		info := channelInfo{
			Platform: pid,
			Name:     meta.Name,
			Icon:     meta.Icon,
			Fields:   meta.Fields,
			Values:   map[string]string{},
			Route:    routeMap[pid],
		}

		if opts, ok := chCfg.Channels[pid]; ok {
			info.Configured = true
			if enabled, ok := opts["enabled"].(bool); ok {
				info.Enabled = enabled
			} else {
				info.Enabled = true // default enabled if key absent
			}
			// Redact secret values.
			for _, f := range meta.Fields {
				if v, ok := opts[f.Key]; ok {
					sv := fmt.Sprintf("%v", v)
					if f.Secret && len(sv) > 4 {
						info.Values[f.Key] = sv[:4] + "****"
					} else if f.Secret {
						info.Values[f.Key] = "****"
					} else {
						info.Values[f.Key] = sv
					}
				}
			}
		}

		result = append(result, info)
	}

	return h.success(req.ID, map[string]any{"channels": result, "platforms": platforms})
}

// handleChannelsSave saves credentials for a single channel.
func (h *Handler) handleChannelsSave(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	platform, _ := req.Params["platform"].(string)
	if strings.TrimSpace(platform) == "" {
		return h.invalidParams(req.ID, "platform is required")
	}
	if _, ok := platforms[platform]; !ok {
		return h.invalidParams(req.ID, "unsupported platform: "+platform)
	}

	credsRaw, _ := req.Params["credentials"]
	raw, err := json.Marshal(credsRaw)
	if err != nil {
		return h.invalidParams(req.ID, "invalid credentials")
	}
	var creds map[string]string
	if err := json.Unmarshal(raw, &creds); err != nil {
		return h.invalidParams(req.ID, "credentials must be a string map")
	}

	path := channelsJSONPath()
	if path == "" {
		return h.internalError(req.ID, "cannot determine home directory")
	}

	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()

	chCfg, err := goim.LoadChannelsJSON(path)
	if err != nil {
		return h.internalError(req.ID, "load channels: "+err.Error())
	}

	existing := chCfg.Channels[platform]
	if existing == nil {
		existing = make(map[string]any)
	}
	for k, v := range creds {
		if v != "" {
			existing[k] = v
		}
	}
	existing["enabled"] = true
	chCfg.Channels[platform] = existing

	if err := goim.SaveChannelsJSON(path, chCfg); err != nil {
		return h.internalError(req.ID, "save channels: "+err.Error())
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

// handleChannelsDelete removes a channel from channels.json.
func (h *Handler) handleChannelsDelete(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	platform, _ := req.Params["platform"].(string)
	if strings.TrimSpace(platform) == "" {
		return h.invalidParams(req.ID, "platform is required")
	}

	path := channelsJSONPath()
	if path == "" {
		return h.internalError(req.ID, "cannot determine home directory")
	}

	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()

	chCfg, err := goim.LoadChannelsJSON(path)
	if err != nil {
		return h.internalError(req.ID, "load channels: "+err.Error())
	}

	if _, ok := chCfg.Channels[platform]; !ok {
		return h.invalidParams(req.ID, "channel not found: "+platform)
	}
	delete(chCfg.Channels, platform)

	if err := goim.SaveChannelsJSON(path, chCfg); err != nil {
		return h.internalError(req.ID, "save channels: "+err.Error())
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

// handleChannelsToggle enables or disables a channel.
func (h *Handler) handleChannelsToggle(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	platform, _ := req.Params["platform"].(string)
	if strings.TrimSpace(platform) == "" {
		return h.invalidParams(req.ID, "platform is required")
	}

	enabled, hasEnabled := req.Params["enabled"].(bool)
	if !hasEnabled {
		return h.invalidParams(req.ID, "enabled is required")
	}

	path := channelsJSONPath()
	if path == "" {
		return h.internalError(req.ID, "cannot determine home directory")
	}

	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()

	chCfg, err := goim.LoadChannelsJSON(path)
	if err != nil {
		return h.internalError(req.ID, "load channels: "+err.Error())
	}

	opts, ok := chCfg.Channels[platform]
	if !ok {
		return h.invalidParams(req.ID, "channel not found: "+platform)
	}
	opts["enabled"] = enabled
	chCfg.Channels[platform] = opts

	if err := goim.SaveChannelsJSON(path, chCfg); err != nil {
		return h.internalError(req.ID, "save channels: "+err.Error())
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

// handleChannelsRouteSet sets the persona route for a channel.
func (h *Handler) handleChannelsRouteSet(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	channel, _ := req.Params["channel"].(string)
	persona, _ := req.Params["persona"].(string)
	if strings.TrimSpace(channel) == "" {
		return h.invalidParams(req.ID, "channel is required")
	}

	if err := h.patchPersonas(func(p *config.PersonasConfig) {
		// Remove existing route for this channel.
		filtered := make([]config.PersonaRoute, 0, len(p.Routes))
		for _, r := range p.Routes {
			if r.Channel != channel {
				filtered = append(filtered, r)
			}
		}
		p.Routes = filtered

		// Add new route if persona is specified.
		if strings.TrimSpace(persona) != "" {
			p.Routes = append(p.Routes, config.PersonaRoute{
				Channel: channel,
				Persona: persona,
			})
		}
	}); err != nil {
		return h.internalError(req.ID, "set channel route: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}
