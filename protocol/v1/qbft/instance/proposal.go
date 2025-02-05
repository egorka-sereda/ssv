package instance

import (
	"fmt"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/protocol/v1/qbft"
	"github.com/bloxapp/ssv/protocol/v1/qbft/pipelines"
)

// ProposalMsgPipeline is the main proposal msg pipeline
func (i *Instance) ProposalMsgPipeline() pipelines.SignedMessagePipeline {
	validationPipeline := i.proposalMsgValidationPipeline()

	// TODO: Add value check.
	return pipelines.Combine(
		pipelines.WrapFunc(validationPipeline.Name(), func(signedMessage *specqbft.SignedMessage) error {
			if err := validationPipeline.Run(signedMessage); err != nil {
				return fmt.Errorf("invalid proposal message: %w", err)
			}
			return nil
		}),
		pipelines.WrapFunc("add proposal msg", func(signedMessage *specqbft.SignedMessage) error {
			i.Logger.Info("received valid proposal message for round",
				zap.Any("sender_ibft_id", signedMessage.GetSigners()),
				zap.Uint64("round", uint64(signedMessage.Message.Round)))

			proposalData, err := signedMessage.Message.GetProposalData()
			if err != nil {
				return fmt.Errorf("could not get proposal data: %w", err)
			}
			i.ContainersMap[specqbft.ProposalMsgType].AddMessage(signedMessage, proposalData.Data)

			return nil
		}),
		i.UponProposalMsg(),
	)
}

func (i *Instance) proposalMsgValidationPipeline() pipelines.SignedMessagePipeline {
	return i.fork.ProposalMsgValidationPipeline(i.ValidatorShare, i.GetState(), i.RoundLeader)
}

/*
UponProposalMsg Algorithm 2 IBFTController pseudocode for process pi: normal case operation
upon receiving a valid ⟨PROPOSAL, λi, ri, value⟩ message m from leader(λi, round) such that:
	JustifyProposal(m) do
		set timer i to running and expire after t(ri)
		broadcast ⟨PREPARE, λi, ri, value⟩
*/
func (i *Instance) UponProposalMsg() pipelines.SignedMessagePipeline {
	return pipelines.WrapFunc("upon proposal msg", func(signedMessage *specqbft.SignedMessage) error {
		i.GetState().ProposalAcceptedForCurrentRound.Store(signedMessage)

		newRound := signedMessage.Message.Round

		if currentRound := i.GetState().GetRound(); signedMessage.Message.Round > currentRound {
			i.Logger.Debug("received future justified proposal, bumping into its round and resetting timer",
				zap.Uint64("current_round", uint64(currentRound)),
				zap.Uint64("future_round", uint64(signedMessage.Message.Round)),
			)
			i.bumpToRound(newRound)
			i.ResetRoundTimer()
		}

		proposalData, err := signedMessage.Message.GetProposalData()
		if err != nil {
			return errors.Wrap(err, "failed to get prepare message")
		}

		// mark state
		i.ProcessStageChange(qbft.RoundStateProposal)

		// broadcast prepare msg
		broadcastMsg, err := i.GeneratePrepareMessage(proposalData.Data)
		if err != nil {
			return errors.Wrap(err, "could not create prepare msg")
		}
		if err := i.SignAndBroadcast(broadcastMsg); err != nil {
			i.Logger.Error("failed to broadcast prepare message", zap.Error(err))
			return err
		}
		return nil
	})
}

// GenerateProposalMessage returns proposal msg
func (i *Instance) GenerateProposalMessage(proposalMsg *specqbft.ProposalData) (specqbft.Message, error) {
	proposalEncodedMsg, err := proposalMsg.Encode()
	if err != nil {
		return specqbft.Message{}, errors.Wrap(err, "failed to encoded proposal message")
	}
	identifier := i.GetState().GetIdentifier()
	return specqbft.Message{
		MsgType:    specqbft.ProposalMsgType,
		Height:     i.GetState().GetHeight(),
		Round:      i.GetState().GetRound(),
		Identifier: identifier[:],
		Data:       proposalEncodedMsg,
	}, nil
}

func (i *Instance) checkExistingProposal(round specqbft.Round) (bool, *specqbft.SignedMessage, error) {
	msgs := i.ContainersMap[specqbft.ProposalMsgType].ReadOnlyMessagesByRound(round)
	if len(msgs) == 1 {
		return true, msgs[0], nil
	} else if len(msgs) > 1 {
		return false, nil, errors.New("multiple proposal msgs, can't decide which one to use")
	}
	return false, nil, nil
}
