package ws

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/service"
	"golang.org/x/crypto/bcrypt"
)

func (h *Hub) handleBotBindRequest(bc *BotConn, msg Message) {
	var payload BotBindRequestPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.ASN <= 0 {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "invalid ASN",
		})
		return
	}

	if payload.TgUserID == 0 {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "missing tg_user_id",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	existing, _ := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if existing != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: fmt.Sprintf("already bound to ASN %d. Use /unbind first.", existing.ASN),
		})
		return
	}

	existingASN, _ := h.botBindings.GetByASN(ctx, payload.ASN)
	if existingASN != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: fmt.Sprintf("ASN %d is already bound to another Telegram account.", payload.ASN),
		})
		return
	}

	if h.lookupASNEmail == nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "binding not available",
		})
		return
	}

	email, err := h.lookupASNEmail(payload.ASN)
	if err != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "ASN not found in DN42 registry",
		})
		return
	}

	code, err := generateBotBindCode()
	if err != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "internal error",
		})
		return
	}

	codeHash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "internal error",
		})
		return
	}

	if h.createLoginCode == nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "binding not available",
		})
		return
	}

	if err := h.createLoginCode(payload.ASN, email, string(codeHash), time.Now().Add(5*time.Minute)); err != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "failed to create verification code",
		})
		return
	}

	if h.sendVerifyCode != nil {
		if err := h.sendVerifyCode(email, payload.ASN, code); err != nil {
			log.WithError(err).WithField("asn", payload.ASN).Error("failed to send bot bind verification code")
			h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
				Success: false, Error: "failed to send verification email",
			})
			return
		}
	}

	h.sendToBot(bc, TypeBotBindPending, msg.ID, BotBindPendingPayload{
		MaskedEmail: service.MaskEmail(email),
		ExpiresIn:   300,
	})
}

func (h *Hub) handleBotBindVerify(bc *BotConn, msg Message) {
	var payload BotBindVerifyPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.TgUserID == 0 || payload.ASN <= 0 || payload.Code == "" {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "missing required fields",
		})
		return
	}

	if h.getValidLoginCode == nil || h.markLoginCodeUsed == nil || h.incrCodeFailures == nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "binding not available",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	loginCode, err := h.getValidLoginCode(ctx, payload.ASN)
	if err != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "no pending verification. Use /bind <ASN> first.",
		})
		return
	}

	if loginCode.ASN != payload.ASN {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "code does not match ASN",
		})
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(loginCode.CodeHash), []byte(payload.Code)) != nil {
		h.incrCodeFailures(ctx, loginCode.ID)
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "invalid verification code",
		})
		return
	}

	h.markLoginCodeUsed(ctx, loginCode.ID)

	binding := &model.BotUserBinding{
		TgUserID:   payload.TgUserID,
		TgUsername: payload.TgUsername,
		ASN:        payload.ASN,
		BoundVia:   "code",
	}
	if err := h.botBindings.Create(ctx, binding); err != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "failed to create binding",
		})
		return
	}

	h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
		Success: true,
		Message: fmt.Sprintf("Successfully bound ASN %d to your Telegram account.", payload.ASN),
	})
}

func (h *Hub) handleBotWebBind(bc *BotConn, msg Message) {
	var payload BotWebBindPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.Token == "" || payload.TgUserID == 0 {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "missing required fields",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token, err := h.botBindings.GetWebBindToken(ctx, payload.Token)
	if err != nil || token == nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "invalid or expired token",
		})
		return
	}

	if token.Used {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "token already used",
		})
		return
	}

	if token.ExpiresAt.Before(time.Now()) {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "token expired",
		})
		return
	}

	existing, _ := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if existing != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: fmt.Sprintf("already bound to ASN %d. Use /unbind first.", existing.ASN),
		})
		return
	}

	existingForASN, _ := h.botBindings.GetByASN(ctx, token.ASN)
	if existingForASN != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: fmt.Sprintf("ASN %d is already bound to another Telegram account.", token.ASN),
		})
		return
	}

	binding := &model.BotUserBinding{
		TgUserID:   payload.TgUserID,
		TgUsername: payload.TgUsername,
		ASN:        token.ASN,
		BoundVia:   "web",
	}
	if err := h.botBindings.Create(ctx, binding); err != nil {
		h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "failed to create binding",
		})
		return
	}

	h.botBindings.MarkWebBindTokenUsed(ctx, payload.Token)

	h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{
		Success: true,
		Message: fmt.Sprintf("Successfully bound ASN %d to your Telegram account.", token.ASN),
	})
}

func (h *Hub) handleBotUnbind(bc *BotConn, msg Message) {
	var payload BotUnbindPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.TgUserID == 0 {
		h.sendToBot(bc, TypeBotUnbindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "missing tg_user_id",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	existing, err := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if err != nil || existing == nil {
		h.sendToBot(bc, TypeBotUnbindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "no binding found",
		})
		return
	}

	if err := h.botBindings.Delete(ctx, payload.TgUserID); err != nil {
		h.sendToBot(bc, TypeBotUnbindResult, msg.ID, BotBindResultPayload{
			Success: false, Error: "failed to unbind",
		})
		return
	}

	h.sendToBot(bc, TypeBotUnbindResult, msg.ID, BotBindResultPayload{
		Success: true,
		Message: fmt.Sprintf("Successfully unbound ASN %d.", existing.ASN),
	})
}

func (h *Hub) handleBotWhoami(bc *BotConn, msg Message) {
	var payload BotWhoamiPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.TgUserID == 0 {
		h.sendToBot(bc, TypeBotWhoamiResult, msg.ID, BotWhoamiResultPayload{
			Bound: false,
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	binding, err := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if err != nil || binding == nil {
		h.sendToBot(bc, TypeBotWhoamiResult, msg.ID, BotWhoamiResultPayload{
			Bound: false,
		})
		return
	}

	h.sendToBot(bc, TypeBotWhoamiResult, msg.ID, BotWhoamiResultPayload{
		Bound:      true,
		ASN:        binding.ASN,
		TgUsername: binding.TgUsername,
		IsAdmin:    binding.IsAdmin,
		BoundVia:   binding.BoundVia,
		BoundAt:    binding.BoundAt.Format(time.RFC3339),
	})
}

func generateBotBindCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
