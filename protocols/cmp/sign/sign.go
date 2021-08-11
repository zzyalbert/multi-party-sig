package sign

import (
	"errors"
	"fmt"

	"github.com/taurusgroup/multi-party-sig/internal/hash"
	"github.com/taurusgroup/multi-party-sig/internal/round"
	"github.com/taurusgroup/multi-party-sig/pkg/math/curve"
	"github.com/taurusgroup/multi-party-sig/pkg/math/polynomial"
	"github.com/taurusgroup/multi-party-sig/pkg/paillier"
	"github.com/taurusgroup/multi-party-sig/pkg/party"
	"github.com/taurusgroup/multi-party-sig/pkg/pedersen"
	"github.com/taurusgroup/multi-party-sig/pkg/pool"
	"github.com/taurusgroup/multi-party-sig/pkg/protocol"
	"github.com/taurusgroup/multi-party-sig/pkg/protocol/types"
	"github.com/taurusgroup/multi-party-sig/protocols/cmp/keygen"
)

// protocolSignID for the "3 round" variant using echo broadcast.
const (
	protocolSignID     types.ProtocolID  = "cmp/sign-3-round-echo"
	protocolSignRounds types.RoundNumber = 5
)

var (
	_ round.Round = (*round1)(nil)
	_ round.Round = (*round2)(nil)
	_ round.Round = (*round3)(nil)
	_ round.Round = (*round4)(nil)
	_ round.Round = (*output)(nil)
)

func StartSign(pl *pool.Pool, config *keygen.Config, signers []party.ID, message []byte) protocol.StartFunc {
	return func() (round.Round, protocol.Info, error) {

		// this could be used to indicate a pre-signature later on
		if len(message) == 0 {
			return nil, nil, errors.New("sign.Create: message is nil")
		}

		signerIDs := party.NewIDSlice(signers)
		if !config.CanSign(signerIDs) {
			return nil, nil, errors.New("sign.Create: signers is not a valid signing subset")
		}

		// validate config
		if err := config.Validate(); err != nil {
			return nil, nil, err
		}

		selfID := config.ID

		helper, err := round.NewHelper(
			protocolSignID,
			protocolSignRounds,
			selfID,
			signerIDs,
			// write the config, the signers and the message to this session.
			config,
			signerIDs,
			hash.BytesWithDomain{
				TheDomain: "Signature Message",
				Bytes:     message,
			},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("sign.Create: %w", err)
		}

		// Scale public data
		T := len(signerIDs)
		ECDSA := make(map[party.ID]*curve.Point, T)
		Paillier := make(map[party.ID]*paillier.PublicKey, T)
		Pedersen := make(map[party.ID]*pedersen.Parameters, T)
		PublicKey := curve.NewIdentityPoint()
		lagrange := polynomial.Lagrange(signers)
		for _, j := range signerIDs {
			public := config.Public[j]
			// scale public key share
			ECDSA[j] = curve.NewIdentityPoint().ScalarMult(lagrange[j], public.ECDSA)
			// create Paillier key
			Paillier[j] = paillier.NewPublicKey(public.N)
			// create Pedersen params
			Pedersen[j] = pedersen.New(public.N, public.S, public.T)
			PublicKey.Add(PublicKey, ECDSA[j])
		}

		// Scale own secret
		SecretECDSA := curve.NewScalar().Multiply(lagrange[config.ID], config.ECDSA)
		SecretPaillier := config.Paillier()
		Paillier[selfID] = SecretPaillier.PublicKey
		Pedersen[selfID].SetCRT(SecretPaillier.CRT())

		return &round1{
			Helper:         helper,
			Pool:           pl,
			PublicKey:      PublicKey,
			SecretECDSA:    SecretECDSA,
			SecretPaillier: config.Paillier(),
			Paillier:       Paillier,
			Pedersen:       Pedersen,
			ECDSA:          ECDSA,
			Message:        message,
		}, helper, nil
	}
}
