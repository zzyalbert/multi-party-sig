package keygen

import (
	"errors"

	"github.com/taurusgroup/multi-party-sig/internal/proto"
	"github.com/taurusgroup/multi-party-sig/internal/round"
	"github.com/taurusgroup/multi-party-sig/pkg/math/curve"
	"github.com/taurusgroup/multi-party-sig/pkg/math/polynomial"
	"github.com/taurusgroup/multi-party-sig/pkg/party"
	"github.com/taurusgroup/multi-party-sig/pkg/protocol/message"
	"github.com/taurusgroup/multi-party-sig/pkg/protocol/types"
	zkmod "github.com/taurusgroup/multi-party-sig/pkg/zk/mod"
	zkprm "github.com/taurusgroup/multi-party-sig/pkg/zk/prm"
)

var _ round.Round = (*round4)(nil)

type round4 struct {
	*round3

	// RID = ⊕ⱼ RIDⱼ
	// Random ID generated by taking the XOR of all ridᵢ
	RID RID
	// ChainKey is a sequence of random bytes agreed upon together
	ChainKey []byte
}

// VerifyMessage implements round.Round.
//
// - verify Mod, Prm proof for N
func (r *round4) VerifyMessage(from party.ID, _ party.ID, content message.Content) error {
	body := content.(*Keygen4)

	// verify zkmod
	if !body.Mod.Verify(r.HashForID(from), zkmod.Public{N: r.N[from]}) {
		return ErrRound4ZKMod
	}

	// verify zkprm
	if !body.Prm.Verify(r.HashForID(from), zkprm.Public{N: r.N[from], S: r.S[from], T: r.T[from]}) {
		return ErrRound4ZKPrm
	}

	return nil
}

// StoreMessage implements round.Round.
//
// Since this message is only intended for us, we need to to the VSS verification here.
// - check that the decrypted share did not overflow.
// - check VSS condition.
// - save share.
func (r *round4) StoreMessage(from party.ID, content message.Content) error {
	body := content.(*Keygen4)
	// decrypt share
	DecryptedShare, err := r.PaillierSecret.Dec(body.Share)
	if err != nil {
		return err
	}
	Share := curve.NewScalarInt(DecryptedShare)
	if DecryptedShare.Eq(Share.Int()) != 1 {
		return ErrRound4Decrypt
	}

	// verify share with VSS
	ExpectedPublicShare := r.VSSPolynomials[from].Evaluate(r.SelfID().Scalar()) // Fⱼ(i)
	PublicShare := curve.NewIdentityPoint().ScalarBaseMult(Share)
	// X == Fⱼ(i)
	if !PublicShare.Equal(ExpectedPublicShare) {
		return ErrRound4VSS
	}

	r.ShareReceived[from] = Share
	return nil
}

// Finalize implements round.Round
//
// - sum of all received shares
// - compute group public key and individual public keys
// - recompute config SSID
// - validate Config
// - write new ssid hash to old hash state
// - create proof of knowledge of secret.
func (r *round4) Finalize(out chan<- *message.Message) (round.Round, error) {
	// add all shares to our secret
	UpdatedSecretECDSA := curve.NewScalar().Set(r.PreviousSecretECDSA)
	for _, j := range r.PartyIDs() {
		UpdatedSecretECDSA.Add(UpdatedSecretECDSA, r.ShareReceived[j])
	}

	// [F₁(X), …, Fₙ(X)]
	ShamirPublicPolynomials := make([]*polynomial.Exponent, 0, len(r.VSSPolynomials))
	for _, VSSPolynomial := range r.VSSPolynomials {
		ShamirPublicPolynomials = append(ShamirPublicPolynomials, VSSPolynomial)
	}

	// ShamirPublicPolynomial = F(X) = ∑Fⱼ(X)
	ShamirPublicPolynomial, err := polynomial.Sum(ShamirPublicPolynomials)
	if err != nil {
		return nil, err
	}

	// compute the new public key share Xⱼ = F(j) (+X'ⱼ if doing a refresh)
	PublicData := make(map[party.ID]*Public, len(r.PartyIDs()))
	for _, j := range r.PartyIDs() {
		PublicECDSAShare := ShamirPublicPolynomial.Evaluate(j.Scalar())
		PublicECDSAShare.Add(PublicECDSAShare, r.PreviousPublicSharesECDSA[j])
		PublicData[j] = &Public{
			ECDSA: PublicECDSAShare,
			N:     r.N[j],
			S:     r.S[j],
			T:     r.T[j],
		}
	}

	UpdatedConfig := &Config{
		Threshold: uint32(r.Threshold),
		Public:    PublicData,
		RID:       r.RID.Copy(),
		ChainKey:  r.ChainKey,
		Secret: &Secret{
			ID:    r.SelfID(),
			ECDSA: UpdatedSecretECDSA,
			P:     &proto.NatMarshaller{Nat: r.PaillierSecret.P()},
			Q:     &proto.NatMarshaller{Nat: r.PaillierSecret.Q()},
		},
	}

	// write new ssid to hash, to bind the Schnorr proof to this new config
	// Write SSID, selfID to temporary hash
	h := r.Hash()
	_ = h.WriteAny(UpdatedConfig, r.SelfID())

	proof := r.SchnorrRand.Prove(h, PublicData[r.SelfID()].ECDSA, UpdatedSecretECDSA)

	// send to all
	msg := r.MarshalMessage(&KeygenOutput{SchnorrResponse: proof}, r.OtherPartyIDs()...)
	if err = r.SendMessage(msg, out); err != nil {
		return r, err
	}

	r.UpdateHashState(UpdatedConfig)
	return &output{
		round4:        r,
		UpdatedConfig: UpdatedConfig,
	}, nil
}

// MessageContent implements round.Round.
func (r *round4) MessageContent() message.Content { return &Keygen4{} }

// Validate implements message.Content.
func (m *Keygen4) Validate() error {
	if m == nil {
		return errors.New("keygen.round4: message is nil")
	}
	if m.Mod == nil {
		return errors.New("keygen.round4: zkmod proof is nil")
	}
	if m.Prm == nil {
		return errors.New("keygen.round4: zkprm proof is nil")
	}
	if m.Share == nil {
		return errors.New("keygen.round4: Share proof is nil")
	}
	return nil
}

// RoundNumber implements message.Content.
func (m *Keygen4) RoundNumber() types.RoundNumber { return 5 }
