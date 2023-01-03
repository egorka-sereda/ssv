package validator

import (
	"encoding/hex"
	"github.com/bloxapp/ssv/protocol/blockchain/beacon"
	types2 "github.com/bloxapp/ssv/protocol/types"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/herumi/bls-eth-go-binary/bls"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/bloxapp/ssv/eth1"
	"github.com/bloxapp/ssv/eth1/abiparser"
	registrystorage "github.com/bloxapp/ssv/registry/storage"
	"github.com/bloxapp/ssv/utils/rsaencryption"
)

// UpdateShareMetadata will update the given share object w/o involving storage,
// it will be called only when a new share is created
func UpdateShareMetadata(share *types2.SSVShare, bc beacon.Beacon) (bool, error) {
	pk := hex.EncodeToString(share.ValidatorPubKey)
	results, err := beacon.FetchValidatorsMetadata(bc, [][]byte{share.ValidatorPubKey})
	if err != nil {
		return false, errors.Wrap(err, "failed to fetch metadata for share")
	}
	meta, ok := results[pk]
	if !ok {
		return false, nil
	}
	share.BeaconMetadata = meta
	return true, nil
}

// ShareFromValidatorEvent takes the contract event data and creates the corresponding validator share.
// share could return nil in case operator key is not present/ different
func ShareFromValidatorEvent(
	validatorRegistrationEvent abiparser.ValidatorRegistrationEvent,
	registryStorage registrystorage.OperatorsCollection,
	shareEncryptionKeyProvider ShareEncryptionKeyProvider,
	operatorPubKey string,
) (*types2.SSVShare, *bls.SecretKey, error) {
	validatorShare := types2.SSVShare{}

	// extract operator public keys from storage and fill the event
	if err := SetOperatorPublicKeys(registryStorage, &validatorRegistrationEvent); err != nil {
		return nil, nil, errors.Wrap(err, "could not set operator public keys")
	}

	publicKey := &bls.PublicKey{}
	if err := publicKey.Deserialize(validatorRegistrationEvent.PublicKey); err != nil {
		return nil, nil, &abiparser.MalformedEventError{
			Err: errors.Wrap(err, "failed to deserialize share public key"),
		}
	}
	validatorShare.ValidatorPubKey = publicKey.Serialize()
	validatorShare.OwnerAddress = validatorRegistrationEvent.OwnerAddress.String()
	var shareSecret *bls.SecretKey

	committee := make([]*spectypes.Operator, 0)
	for i := range validatorRegistrationEvent.OperatorPublicKeys {
		nodeID := spectypes.OperatorID(validatorRegistrationEvent.OperatorIds[i])
		committee = append(committee, &spectypes.Operator{
			OperatorID: nodeID,
			PubKey:     validatorRegistrationEvent.SharesPublicKeys[i],
		})
		if strings.EqualFold(string(validatorRegistrationEvent.OperatorPublicKeys[i]), operatorPubKey) {
			validatorShare.OperatorID = nodeID
			validatorShare.SharePubKey = validatorRegistrationEvent.SharesPublicKeys[i]

			operatorPrivateKey, found, err := shareEncryptionKeyProvider()
			if err != nil {
				return nil, nil, errors.Wrap(err, "could not get operator private key")
			}
			if !found {
				return nil, nil, errors.New("could not find operator private key")
			}

			shareSecret = &bls.SecretKey{}
			decryptedSharePrivateKey, err := rsaencryption.DecodeKey(operatorPrivateKey, string(validatorRegistrationEvent.EncryptedKeys[i]))
			if err != nil {
				return nil, nil, &abiparser.MalformedEventError{
					Err: errors.Wrap(err, "failed to decrypt share private key"),
				}
			}
			decryptedSharePrivateKey = strings.Replace(decryptedSharePrivateKey, "0x", "", 1)
			if err := shareSecret.SetHexString(decryptedSharePrivateKey); err != nil {
				return nil, nil, &abiparser.MalformedEventError{
					Err: errors.Wrap(err, "failed to set decrypted share private key"),
				}
			}
		}
	}

	f := uint64(len(committee)-1) / 3
	validatorShare.Quorum = 3 * f
	validatorShare.PartialQuorum = 2 * f
	validatorShare.DomainType = types2.GetDefaultDomain()
	validatorShare.Committee = committee
	validatorShare.SetOperators(validatorRegistrationEvent.OperatorPublicKeys)
	validatorShare.Graffiti = []byte("ssv.network")

	return &validatorShare, shareSecret, nil
}

// SetOperatorPublicKeys extracts the operator public keys from the storage and fill the event
func SetOperatorPublicKeys(
	registryStorage registrystorage.OperatorsCollection,
	validatorRegistrationEvent *abiparser.ValidatorRegistrationEvent,
) error {
	validatorRegistrationEvent.OperatorPublicKeys = make([][]byte, len(validatorRegistrationEvent.OperatorIds))
	for i, operatorID := range validatorRegistrationEvent.OperatorIds {
		od, found, err := registryStorage.GetOperatorData(uint64(operatorID))
		if err != nil {
			return errors.Wrap(err, "could not get operator's data")
		}
		if !found {
			return &abiparser.MalformedEventError{
				Err: errors.New("could not find operator data by index"),
			}
		}
		validatorRegistrationEvent.OperatorPublicKeys[i] = []byte(od.PublicKey)
	}
	return nil
}

func LoadLocalEvents(logger *zap.Logger, handler eth1.SyncEventHandler, path string) error {
	yamlFile, err := ioutil.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}

	var parsedData []*eth1.Event
	err = yaml.Unmarshal(yamlFile, &parsedData)
	if err != nil {
		return err
	}
	for _, ev := range parsedData {
		logFields, err := handler(*ev)
		errs := eth1.HandleEventResult(logger, *ev, logFields, err, false)
		if len(errs) > 0 {
			logger.Warn("could not handle some of the events during local events sync", zap.Any("errs", errs))
			return errors.New("could not handle some of the events during local events sync")
		}
	}

	logger.Info("managed to sync local events")
	return nil
}
