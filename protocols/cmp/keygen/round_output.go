package keygen

import (
	"errors"

	"github.com/taurusgroup/multi-party-sig/internal/round"
	"github.com/taurusgroup/multi-party-sig/pkg/party"
	"github.com/taurusgroup/multi-party-sig/pkg/protocol/message"
	"github.com/taurusgroup/multi-party-sig/pkg/protocol/types"
)

var _ round.Round = (*output)(nil)

type output struct {
	*round4
	UpdatedConfig *Config
}

// VerifyMessage implements round.Round.
//
// - verify all Schnorr proof for the new ecdsa share.
func (r *output) VerifyMessage(from party.ID, to party.ID, content message.Content) error {
	body := content.(*KeygenOutput)

	if !body.SchnorrResponse.Verify(r.HashForID(from),
		r.UpdatedConfig.Public[from].ECDSA,
		r.SchnorrCommitments[from]) {
		return ErrRoundOutputZKSch
	}
	return nil
}

// StoreMessage implements round.Round.
func (r *output) StoreMessage(party.ID, message.Content) error { return nil }

// Finalize implements round.Round.
func (r *output) Finalize(chan<- *message.Message) (round.Round, error) {
	return &round.Output{Result: &Result{
		Config: r.UpdatedConfig,
	}}, nil
}

// MessageContent implements round.Round.
func (r *output) MessageContent() message.Content { return &KeygenOutput{} }

// Validate implements message.Content.
func (m *KeygenOutput) Validate() error {
	if m == nil {
		return errors.New("keygen.output: message is nil")
	}
	if m.SchnorrResponse == nil {
		return errors.New("keygen.output: sch proof is nil")
	}
	return nil
}

// RoundNumber implements message.Content.
func (m *KeygenOutput) RoundNumber() types.RoundNumber { return 6 }
