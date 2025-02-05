package prepare

import (
	"bytes"
	"fmt"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	"github.com/pkg/errors"

	"github.com/bloxapp/ssv/protocol/v1/qbft"
	"github.com/bloxapp/ssv/protocol/v1/qbft/pipelines"
)

// ErrInvalidSignersNum represents an error when the number of signers is invalid.
var ErrInvalidSignersNum = errors.New("prepare msg allows 1 signer")

// ValidatePrepareMsgSigners validates prepare message signers.
func ValidatePrepareMsgSigners() pipelines.SignedMessagePipeline {
	return pipelines.WrapFunc("validate prepare", func(signedMessage *specqbft.SignedMessage) error {
		signers := signedMessage.GetSigners()
		if len(signers) != 1 {
			return ErrInvalidSignersNum
		}

		return nil
	})
}

// ValidateProposal validates message against received proposal for this round.
func ValidateProposal(state *qbft.State) pipelines.SignedMessagePipeline {
	return pipelines.WrapFunc("validate proposal", func(signedMessage *specqbft.SignedMessage) error {
		proposedMsg := state.GetProposalAcceptedForCurrentRound()
		if proposedMsg == nil {
			return fmt.Errorf("no proposal accepted for prepare")
		}

		proposedData, err := proposedMsg.Message.GetProposalData()
		if err != nil {
			return fmt.Errorf("could not get proposed data: %w", err)
		}

		prepareData, err := signedMessage.Message.GetPrepareData()
		if err != nil {
			return fmt.Errorf("could not get prepare data: %w", err)
		}
		if err := prepareData.Validate(); err != nil {
			return fmt.Errorf("prepareData invalid: %w", err)
		}

		if !bytes.Equal(prepareData.Data, proposedData.Data) {
			return fmt.Errorf("message data is different from proposed data")
		}

		return nil
	})
}
