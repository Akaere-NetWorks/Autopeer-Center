package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

func bootstrapAdmin(ctx context.Context, adminRepo repository.AdminRepository, cfg *config.Config) {
	if cfg.AdminInitialEmail == "" || cfg.AdminInitialPassword == "" {
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminInitialPassword), bcrypt.DefaultCost)
	if err != nil {
		log.WithError(err).Error("failed to hash admin password")
		return
	}

	err = adminRepo.Upsert(ctx, &model.Admin{
		Email:        cfg.AdminInitialEmail,
		PasswordHash: string(hash),
	})
	if err != nil {
		log.WithError(err).Error("failed to upsert admin")
		return
	}
	log.WithField("email", cfg.AdminInitialEmail).Info("admin account created or password updated")
}

type centerKeyFile struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

func loadOrGenerateCenterKeyPair(path string) (*crypto.KeyPair, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var kf centerKeyFile
		if err := json.Unmarshal(data, &kf); err == nil {
			priv, err := crypto.PrivateKeyFromHex(kf.PrivateKey)
			if err == nil {
				log.WithField("path", path).Info("loaded center key pair from disk")
				return &crypto.KeyPair{PrivateKey: priv, PublicKey: priv.PublicKey()}, nil
			}
		}
		log.WithError(err).Warn("failed to parse center key file, generating new key pair")
	}

	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.WithError(err).Warn("failed to create directory for center key pair, key will not persist")
		return kp, nil
	}

	kf := centerKeyFile{
		PrivateKey: crypto.PrivKeyHex(kp.PrivateKey),
		PublicKey:  crypto.PubKeyHex(kp.PublicKey),
	}
	data, err = json.Marshal(kf)
	if err != nil {
		log.WithError(err).Warn("failed to marshal center key pair")
		return kp, nil
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.WithError(err).Warn("failed to save center key pair, key will not persist across restarts")
	} else {
		log.WithField("path", path).Info("saved center key pair to disk")
	}

	return kp, nil
}
