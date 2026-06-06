package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/akaere/autopeer-center/internal/model"
)

func (h *MCPHandler) handleToolNodesList(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	return h.toolNodesList(ctx)
}

func (h *MCPHandler) handleToolPeerCreationStatus(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	return h.toolPeerCreationStatus(ctx)
}

func (h *MCPHandler) handleToolPeerList(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	return h.toolPeerList(ctx, asn)
}

func (h *MCPHandler) handleToolPeerSummary(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	return h.toolPeerSummary(ctx, asn)
}

func (h *MCPHandler) handleToolPeerGet(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.toolPeerGet(ctx, asn, args.ID)
}

func (h *MCPHandler) handleToolPeerGetMetrics(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
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
	return h.toolPeerGetMetrics(ctx, asn, args.ID, hours)
}

func (h *MCPHandler) handleToolUserMCPAuditLogs(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
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
	return h.mcp.ListAuditLogsByASN(ctx, asn, limit)
}

func (h *MCPHandler) handleToolOperationsList(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
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
	return h.mcp.ListOperationsByASN(ctx, asn, limit)
}

func (h *MCPHandler) handleToolOperationGet(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.mcp.GetOperationByASN(ctx, asn, args.ID)
}

func (h *MCPHandler) handleToolPeerCreate(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	var args struct {
		NodeID         string `json:"node_id"`
		RemotePubkey   string `json:"remote_pubkey"`
		RemoteEndpoint string `json:"remote_endpoint"`
		RemoteLLA      string `json:"remote_lla"`
		MTU            *int   `json:"mtu"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.toolPeerCreate(ctx, asn, args.NodeID, args.RemotePubkey, args.RemoteEndpoint, args.RemoteLLA, args.MTU)
}

func (h *MCPHandler) handleToolPeerUpdatePending(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	var args struct {
		ID             string  `json:"id"`
		RemotePubkey   *string `json:"remote_pubkey"`
		RemoteEndpoint *string `json:"remote_endpoint"`
		RemoteLLA      *string `json:"remote_lla"`
		MTU            *int    `json:"mtu"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	return h.toolPeerUpdatePending(ctx, asn, args.ID, args.RemotePubkey, args.RemoteEndpoint, args.RemoteLLA, args.MTU)
}

func (h *MCPHandler) handleToolPeerCancelPending(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	var args struct {
		ID             string `json:"id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	peer, err := h.peers.GetByIDAndASN(ctx, args.ID, asn)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}
	if peer.Status != "pending" && peer.Status != "rejected" {
		return nil, fmt.Errorf("only pending or rejected peers can be cancelled by MCP")
	}
	if err := h.peers.Delete(ctx, args.ID); err != nil {
		return nil, fmt.Errorf("failed to cancel peer: %w", err)
	}
	return map[string]interface{}{"id": args.ID, "status": "cancelled", "previous_status": peer.Status}, nil
}

func (h *MCPHandler) handleToolPeerUpdateOperationCreate(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	var args struct {
		ID             string  `json:"id"`
		RemotePubkey   *string `json:"remote_pubkey"`
		RemoteEndpoint *string `json:"remote_endpoint"`
		RemoteLLA      *string `json:"remote_lla"`
		MTU            *int    `json:"mtu"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	peer, err := h.peers.GetByIDAndASN(ctx, args.ID, asn)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}
	if peer.Status != "active" && peer.Status != "suspended" {
		return nil, fmt.Errorf("operation is only required for active or suspended peers")
	}
	return h.createPeerOperation(ctx, asn, "peer_update", args.ID, raw, args.IdempotencyKey, requestHash)
}

func (h *MCPHandler) handleToolPeerDeleteOperationCreate(ctx context.Context, asn int64, raw json.RawMessage, def *mcpToolDefinition, requestHash string) (interface{}, error) {
	var args struct {
		ID             string `json:"id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	peer, err := h.peers.GetByIDAndASN(ctx, args.ID, asn)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}
	if peer.Status != "active" && peer.Status != "suspended" {
		return nil, fmt.Errorf("operation is only required for active or suspended peers")
	}
	return h.createPeerOperation(ctx, asn, "peer_delete", args.ID, raw, args.IdempotencyKey, requestHash)
}

func (h *MCPHandler) createPeerOperation(ctx context.Context, asn int64, operationType, peerID string, raw json.RawMessage, idemKey, requestHash string) (interface{}, error) {
	var requested map[string]interface{}
	_ = json.Unmarshal(raw, &requested)
	asnCopy := asn
	var keyIDPtr *string
	if keyID, ok := ctx.Value(mcpContextKeyKeyID).(string); ok && keyID != "" {
		keyIDPtr = &keyID
	}
	op := &model.MCPOperation{ActorType: "user", KeyID: keyIDPtr, ASN: &asnCopy, OperationType: operationType, ResourceType: "peer", ResourceID: peerID, RequestedArgs: requested, Status: "pending_admin_review", IdempotencyKey: idemKey, RequestHash: requestHash}
	if err := h.mcp.CreateOperation(ctx, op); err != nil {
		return nil, fmt.Errorf("failed to create operation: %w", err)
	}
	return map[string]interface{}{"operation_id": op.ID, "status": op.Status, "resource_type": op.ResourceType, "resource_id": op.ResourceID, "created_at": op.CreatedAt}, nil
}

func (h *MCPHandler) toolPeerUpdatePending(ctx context.Context, asn int64, id string, pubkey, endpoint, lla *string, mtu *int) (interface{}, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	cur, err := h.peers.GetByIDAndASN(ctx, id, asn)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}
	if cur.Status != "pending" && cur.Status != "rejected" {
		return nil, fmt.Errorf("only pending or rejected peers can be updated by this tool")
	}
	if pubkey == nil && endpoint == nil && lla == nil && mtu == nil {
		return nil, fmt.Errorf("at least one field must be provided")
	}
	if pubkey != nil && (!base64Re.MatchString(*pubkey) || len(*pubkey) < 40) {
		return nil, fmt.Errorf("invalid WireGuard public key")
	}
	if endpoint != nil && !validateEndpoint(*endpoint) {
		return nil, fmt.Errorf("invalid endpoint format")
	}
	if lla != nil && !validateLLA(*lla) {
		return nil, fmt.Errorf("invalid link-local address")
	}
	if mtu != nil && (*mtu < 576 || *mtu > 9000) {
		return nil, fmt.Errorf("MTU must be between 576 and 9000")
	}
	fields := map[string]interface{}{"updated_at": "now()"}
	if pubkey != nil {
		fields["remote_pubkey"] = *pubkey
	}
	if endpoint != nil {
		fields["remote_endpoint"] = *endpoint
	}
	if lla != nil {
		fields["remote_lla"] = *lla
	}
	if mtu != nil {
		fields["mtu"] = *mtu
	}
	if err := h.peers.UpdateFields(ctx, id, fields); err != nil {
		return nil, fmt.Errorf("failed to update peer: %w", err)
	}
	return map[string]interface{}{"id": id, "status": "updated", "previous_status": cur.Status}, nil
}
