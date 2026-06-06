package handler

import (
	"context"
	"encoding/json"
	"fmt"
)

func (h *AdminMCPHandler) handleAdminToolNodesList(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	return h.adminToolNodesList(ctx)
}

func (h *AdminMCPHandler) handleAdminToolNodesStats(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	return h.adminToolNodesStats(ctx)
}

func (h *AdminMCPHandler) handleAdminToolPeersListAll(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		Status  *string `json:"status"`
		Page    *int    `json:"page"`
		PerPage *int    `json:"per_page"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	page, perPage := normalizePage(args.Page, args.PerPage)
	return h.adminToolPeersListAll(ctx, args.Status, page, perPage)
}

func (h *AdminMCPHandler) handleAdminToolPeersListByASN(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		ASN int64 `json:"asn"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.adminToolPeersListByASN(ctx, args.ASN)
}

func (h *AdminMCPHandler) handleAdminToolPeerGet(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.adminToolPeerGetSummary(ctx, args.ID)
}

func (h *AdminMCPHandler) handleAdminToolPeerGetSensitive(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.adminToolPeerGet(ctx, args.ID)
}

func (h *AdminMCPHandler) handleAdminToolPeerMetrics(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		ID    string `json:"id"`
		Hours *int   `json:"hours"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	hours := 24
	if args.Hours != nil {
		if *args.Hours < 1 || *args.Hours > 720 {
			return nil, fmt.Errorf("hours must be between 1 and 720")
		}
		hours = *args.Hours
	}
	return h.adminToolPeerMetrics(ctx, args.ID, hours)
}

func (h *AdminMCPHandler) handleAdminToolUsersList(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	return h.adminToolUsersList(ctx)
}

func (h *AdminMCPHandler) handleAdminToolMCPKeysList(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		ASN *int64 `json:"asn"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.adminToolMCPKeysList(ctx, args.ASN)
}

func (h *AdminMCPHandler) handleAdminToolMCPSessionsList(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		Limit *int `json:"limit"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	limit := 50
	if args.Limit != nil && *args.Limit >= 1 && *args.Limit <= 200 {
		limit = *args.Limit
	}
	return h.adminToolMCPSessionsList(ctx, limit)
}

func (h *AdminMCPHandler) handleAdminToolAuditLogsList(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	var args struct {
		ASN     *int64  `json:"asn"`
		Tool    *string `json:"tool"`
		Page    *int    `json:"page"`
		PerPage *int    `json:"per_page"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	page, perPage := normalizePage(args.Page, args.PerPage)
	return h.adminToolAuditLogsList(ctx, args.ASN, args.Tool, page, perPage)
}

func (h *AdminMCPHandler) handleAdminToolSiteSettingsGet(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	return h.adminToolSiteSettingsGet(ctx)
}

func (h *AdminMCPHandler) handleAdminToolNotificationSettingsGet(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	return h.settings.GetAllNotificationSettings(ctx)
}

func (h *AdminMCPHandler) handleAdminToolReleasesList(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	if h.releases == nil {
		return []interface{}{}, nil
	}
	return h.releases.List(ctx)
}

func (h *AdminMCPHandler) handleAdminToolBotSettingsGet(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	if h.bot == nil {
		return []interface{}{}, nil
	}
	settings, err := h.bot.LoadAllSettings(ctx)
	if err != nil {
		return nil, err
	}
	for _, setting := range settings {
		setting.SettingValueHash = nil
		if setting.SettingKey == "bot_auth_token" {
			setting.SettingValue = ""
		}
	}
	return settings, nil
}

func (h *AdminMCPHandler) handleAdminToolBotStatsGet(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	if h.bot == nil {
		return map[string]interface{}{}, nil
	}
	total, unique, commands, err := h.bot.GetCommandCounts(ctx)
	if err != nil {
		return nil, err
	}
	last, _ := h.bot.GetLastCommandAt(ctx)
	return map[string]interface{}{"total_commands": total, "unique_users": unique, "commands": commands, "last_command_at": last}, nil
}

func (h *AdminMCPHandler) handleAdminToolBotBlockedList(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	if h.bot == nil {
		return []interface{}{}, nil
	}
	return h.bot.ListBlockedUsers(ctx)
}

func (h *AdminMCPHandler) handleAdminToolSystemStatsGet(ctx context.Context, raw json.RawMessage, def *adminMCPToolDefinition) (interface{}, error) {
	if h.stats == nil {
		return map[string]interface{}{}, nil
	}
	return h.stats.Admin(ctx)
}

func normalizePage(pagePtr, perPagePtr *int) (int, int) {
	page, perPage := 1, 50
	if pagePtr != nil && *pagePtr >= 1 {
		page = *pagePtr
	}
	if perPagePtr != nil && *perPagePtr >= 1 && *perPagePtr <= 100 {
		perPage = *perPagePtr
	}
	return page, perPage
}
