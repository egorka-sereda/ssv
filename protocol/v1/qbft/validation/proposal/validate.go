package proposal

import (
	"bytes"
	"fmt"
	"github.com/bloxapp/ssv/utils/logex"
	"go.uber.org/zap"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/pkg/errors"

	"github.com/bloxapp/ssv/protocol/v1/blockchain/beacon"
	"github.com/bloxapp/ssv/protocol/v1/qbft"
	"github.com/bloxapp/ssv/protocol/v1/qbft/pipelines"
	"github.com/bloxapp/ssv/protocol/v1/qbft/validation/changeround"
	"github.com/bloxapp/ssv/protocol/v1/qbft/validation/signedmsg"
	"github.com/bloxapp/ssv/protocol/v1/types"
)

// ErrInvalidSignersNum represents an error when the number of signers is invalid.
var ErrInvalidSignersNum = errors.New("proposal msg allows 1 signer")

// LeaderResolver resolves round's leader
type LeaderResolver func(round specqbft.Round) uint64

// ValidateProposalMsg validates proposal message
func ValidateProposalMsg(share *beacon.Share, state *qbft.State, resolver LeaderResolver) pipelines.SignedMessagePipeline {
	return pipelines.WrapFunc("validate proposal", func(signedMessage *specqbft.SignedMessage) error {
		signers := signedMessage.GetSigners()
		if len(signers) != 1 {
			return ErrInvalidSignersNum
		}

		leader := spectypes.OperatorID(resolver(signedMessage.Message.Round))
		if !signedMessage.MatchedSigners([]spectypes.OperatorID{leader}) {
			return fmt.Errorf("proposal leader invalid")
		}

		proposalData, err := signedMessage.Message.GetProposalData()
		if err != nil {
			return fmt.Errorf("could not get proposal data: %w", err)
		}

		if err := proposalData.Validate(); err != nil {
			return errors.Wrap(err, "proposalData invalid")
		}

		if err := Justify(share, state, signedMessage.Message.Round, proposalData.RoundChangeJustification, proposalData.PrepareJustification, proposalData.Data); err != nil {
			return fmt.Errorf("proposal not justified: %w", err)
		}

		proposalAcceptedForCurrentRound := state.GetProposalAcceptedForCurrentRound()
		round := state.GetRound()
		if (proposalAcceptedForCurrentRound == nil && signedMessage.Message.Round == round) ||
			(proposalAcceptedForCurrentRound != nil && signedMessage.Message.Round > round) {
			return nil
		}
		return errors.New("proposal is not valid with current state")
	})
}

// Justify implements:
// predicate JustifyProposal(hPROPOSAL, λi, round, value)
// 	return
// 		round = 1
// 		∨ received a quorum Qrc of valid <ROUND-CHANGE, λi, round, prj , pvj> messages such that:
// 			∀ <ROUND-CHANGE, λi, round, prj , pvj> ∈ Qrc : prj = ⊥ ∧ prj = ⊥
// 			∨ received a quorum of valid <PREPARE, λi, pr, value> messages such that:
// 				(pr, value) = HighestPrepared(Qrc)
// TODO: move its tests to this package
func Justify(share *beacon.Share, state *qbft.State, round specqbft.Round, roundChanges, prepares []*specqbft.SignedMessage, proposedValue []byte) error {
	// TODO: add value check
	if round == 1 {
		return nil
	}

	for _, rc := range roundChanges {
		// TODO: refactor
		if err := pipelines.Combine(
			signedmsg.BasicMsgValidation(),
			signedmsg.MsgTypeCheck(specqbft.RoundChangeMsgType),
			signedmsg.ValidateSequenceNumber(state.GetHeight()),
			signedmsg.ValidateRound(round),
			signedmsg.AuthorizeMsg(share),
			changeround.ValidateWithRound(share, round),
		).Run(rc); err != nil {
			return fmt.Errorf("change round msg not valid: %w", err)
		}
	}

	if quorum, _, _ := signedmsg.HasQuorum(share, roundChanges); !quorum {
		return errors.New("change round has not quorum")
	}

	// previouslyPreparedF returns true if any on the round change messages have a prepared round and value
	previouslyPrepared, err := func(rcMsgs []*specqbft.SignedMessage) (bool, error) {
		for _, rc := range rcMsgs {
			rcData, err := rc.Message.GetRoundChangeData()
			if err != nil {
				return false, fmt.Errorf("could not get round change data: %w", err)
			}
			if rcData.Prepared() {
				return true, nil
			}
		}
		return false, nil
	}(roundChanges)
	if err != nil {
		return fmt.Errorf("could not calculate if previously prepared: %w", err)
	}

	if !previouslyPrepared {
		return nil
	}

	if quorum, _, _ := signedmsg.HasQuorum(share, prepares); !quorum {
		return errors.New("prepares has no quorum")
	}

	// get a round change data for which there is a justification for the highest previously prepared round
	highest, err := highestPrepared(roundChanges)
	if err != nil {
		return errors.Wrap(err, "could not get highest prepared")
	}

	if highest == nil {
		return errors.New("no highest prepared")
	}

	highestData, err := highest.Message.GetRoundChangeData()
	if err != nil {
		return errors.Wrap(err, "could not get round change data")
	}

	// proposed value must equal highest prepared value
	if !bytes.Equal(proposedValue, highestData.PreparedValue) {
		return errors.New("proposed data doesn't match highest prepared")
	}

	for _, rcj := range prepares {
		if err := validateRoundChangeJustification(
			rcj,
			state.GetHeight(),
			highestData.PreparedRound,
			highestData.PreparedValue,
			share,
		); err != nil {
			logex.GetLogger().Debug("validate change round justification error", zap.Error(err))
			return fmt.Errorf("signed prepare not valid")
		}
	}

	return nil
}

// highestPrepared returns a round change message with the highest prepared round, returns nil if none found
func highestPrepared(roundChanges []*specqbft.SignedMessage) (*specqbft.SignedMessage, error) {
	var ret *specqbft.SignedMessage
	for _, rc := range roundChanges {
		rcData, err := rc.Message.GetRoundChangeData()
		if err != nil {
			return nil, errors.Wrap(err, "could not get round change data")
		}

		if !rcData.Prepared() {
			continue
		}

		if ret == nil {
			ret = rc
		} else {
			retRCData, err := ret.Message.GetRoundChangeData()
			if err != nil {
				return nil, errors.Wrap(err, "could not get round change data")
			}
			if retRCData.PreparedRound < rcData.PreparedRound {
				ret = rc
			}
		}
	}
	return ret, nil
}

// TODO: merge with (*validateJustification).validateRoundChangeJustification, use it everywhere where spec does
func validateRoundChangeJustification(
	signedPrepare *specqbft.SignedMessage,
	height specqbft.Height,
	round specqbft.Round,
	value []byte,
	share *beacon.Share,
) error {
	if signedPrepare.Message.MsgType != specqbft.PrepareMsgType {
		return errors.New("prepare msg type is wrong")
	}
	if signedPrepare.Message.Height != height {
		return errors.New("message height is wrong")
	}
	if signedPrepare.Message.Round != round {
		return errors.New("round is wrong")
	}

	prepareData, err := signedPrepare.Message.GetPrepareData()
	if err != nil {
		return errors.Wrap(err, "could not get prepare data")
	}
	if err := prepareData.Validate(); err != nil {
		return errors.Wrap(err, "prepareData invalid")
	}

	if !bytes.Equal(prepareData.Data, value) {
		return errors.New("prepare data != proposed data")
	}

	// support old version tag v0.3.1 TODO need to remove and check singleSigner only on version tag v0.3.3
	qourum := share.HasQuorum(len(signedPrepare.GetSigners()))
	singleSigner := len(signedPrepare.GetSigners()) == 1
	if !qourum && !singleSigner {
		return errors.New("prepare msg allows 1 signer")
	}

	// TODO need to return on version v0.3.3
	/*if len(signedPrepare.GetSigners()) != 1 {
		return errors.New("prepare msg allows 1 signer")
	}*/

	// validateJustification justification signature
	pksMap, err := share.PubKeysByID(signedPrepare.GetSigners())
	var pks beacon.PubKeys
	for _, v := range pksMap {
		pks = append(pks, v)
	}

	if err != nil {
		return errors.Wrap(err, "change round could not get pubkey")
	}
	aggregated := pks.Aggregate()

	if err = signedPrepare.Signature.Verify(signedPrepare, types.GetDefaultDomain(), spectypes.QBFTSignatureType, aggregated.Serialize()); err != nil {
		return errors.Wrap(err, "invalid message signature")
	}

	return nil
}
