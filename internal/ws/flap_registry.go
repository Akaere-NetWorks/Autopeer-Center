package ws

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/repository"
)

// FlapRegistry resolves and authenticates flap-agent identities. Flap agents are
// not Nodes; their identities live in the flap_agents table (managed by admins),
// not in the node registry. Authentication mirrors the node agent: a bearer
// token bootstraps the connection, then an X25519 key exchange pins the agent's
// public key (TOFU) and encrypts the session.
type FlapRegistry interface {
	// ValidateToken returns the agent id bound to an enabled token, if any.
	ValidateToken(ctx context.Context, token string) (agentID string, ok bool)
	// PubkeyFor returns the pinned hex X25519 public key for an agent id. The
	// second result is false only on lookup error; an empty string with ok=true
	// means "no key pinned yet" (first connect).
	PubkeyFor(ctx context.Context, agentID string) (pubkeyHex string, ok bool)
	// PinPubkey stores the agent's public key on first connect (TOFU). It is a
	// no-op when a key is already pinned. Returns true when a key was written.
	PinPubkey(ctx context.Context, agentID, pubkeyHex string) (stored bool, err error)
	// TouchSeen records the agent's last-seen time and advertised version.
	TouchSeen(ctx context.Context, agentID, version string)
}

// DBFlapRegistry is a flap_agents-table-backed FlapRegistry.
type DBFlapRegistry struct {
	repo repository.FlapAgentRepository
}

// NewDBFlapRegistry builds a registry backed by the flap_agents repository.
func NewDBFlapRegistry(repo repository.FlapAgentRepository) *DBFlapRegistry {
	return &DBFlapRegistry{repo: repo}
}

func (r *DBFlapRegistry) ValidateToken(ctx context.Context, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	agent, err := r.repo.GetByToken(ctx, token)
	if err != nil {
		return "", false
	}
	return agent.AgentID, true
}

func (r *DBFlapRegistry) PubkeyFor(ctx context.Context, agentID string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	agent, err := r.repo.GetByAgentID(ctx, agentID)
	if err != nil {
		return "", false
	}
	return agent.AgentPubkey, true
}

func (r *DBFlapRegistry) PinPubkey(ctx context.Context, agentID, pubkeyHex string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := r.repo.SetPubkey(ctx, agentID, pubkeyHex)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *DBFlapRegistry) TouchSeen(ctx context.Context, agentID, version string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.repo.TouchSeen(ctx, agentID, version); err != nil {
		flog.WithError(err).WithField("agent_id", agentID).Debug("flap touch-seen failed")
	}
}
