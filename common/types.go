package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"

	"github.com/attestantio/go-builder-client/api"
	"github.com/attestantio/go-builder-client/api/capella"
	apiv1 "github.com/attestantio/go-builder-client/api/v1"
	"github.com/attestantio/go-builder-client/spec"
	apiv1capella "github.com/attestantio/go-eth2-client/api/v1/capella"
	consensusspec "github.com/attestantio/go-eth2-client/spec"
	consensuscapella "github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	boostTypes "github.com/flashbots/go-boost-utils/types"
)

var (
	ErrUnknownNetwork = errors.New("unknown network")
	ErrEmptyPayload   = errors.New("empty payload")

	EthNetworkRopsten  = "ropsten"
	EthNetworkSepolia  = "sepolia"
	EthNetworkGoerli   = "goerli"
	EthNetworkMainnet  = "mainnet"
	EthNetworkZhejiang = "zhejiang"
	EthNetworkCustom   = "custom"

	CapellaForkVersionRopsten = "0x03001020"
	CapellaForkVersionSepolia = "0x90000072"
	CapellaForkVersionGoerli  = "0x03001020"
	CapellaForkVersionMainnet = "0x03000000"

	// Zhejiang details
	GenesisForkVersionZhejiang    = "0x00000069"
	GenesisValidatorsRootZhejiang = "0x53a92d8f2bb1d85f62d16a156e6ebcd1bcaba652d0900b2c2f387826f3481f6f"
	BellatrixForkVersionZhejiang  = "0x00000071"
	CapellaForkVersionZhejiang    = "0x00000072"

	ForkVersionStringBellatrix = "bellatrix"
	ForkVersionStringCapella   = "capella"
	ForkVersionStringDeneb     = "deneb"
)

type EthNetworkDetails struct {
	Name                     string
	GenesisForkVersionHex    string
	GenesisValidatorsRootHex string
	BellatrixForkVersionHex  string
	CapellaForkVersionHex    string

	DomainBuilder                 boostTypes.Domain
	DomainBeaconProposerBellatrix boostTypes.Domain
	DomainBeaconProposerCapella   boostTypes.Domain
}

func NewEthNetworkDetails(networkName string) (ret *EthNetworkDetails, err error) {
	var genesisForkVersion string
	var genesisValidatorsRoot string
	var bellatrixForkVersion string
	var capellaForkVersion string
	var domainBuilder boostTypes.Domain
	var domainBeaconProposerBellatrix boostTypes.Domain
	var domainBeaconProposerCapella boostTypes.Domain

	switch networkName {
	case EthNetworkRopsten:
		genesisForkVersion = boostTypes.GenesisForkVersionRopsten
		genesisValidatorsRoot = boostTypes.GenesisValidatorsRootRopsten
		bellatrixForkVersion = boostTypes.BellatrixForkVersionRopsten
		capellaForkVersion = CapellaForkVersionRopsten
	case EthNetworkSepolia:
		genesisForkVersion = boostTypes.GenesisForkVersionSepolia
		genesisValidatorsRoot = boostTypes.GenesisValidatorsRootSepolia
		bellatrixForkVersion = boostTypes.BellatrixForkVersionSepolia
		capellaForkVersion = CapellaForkVersionSepolia
	case EthNetworkGoerli:
		genesisForkVersion = boostTypes.GenesisForkVersionGoerli
		genesisValidatorsRoot = boostTypes.GenesisValidatorsRootGoerli
		bellatrixForkVersion = boostTypes.BellatrixForkVersionGoerli
		capellaForkVersion = CapellaForkVersionGoerli
	case EthNetworkMainnet:
		genesisForkVersion = boostTypes.GenesisForkVersionMainnet
		genesisValidatorsRoot = boostTypes.GenesisValidatorsRootMainnet
		bellatrixForkVersion = boostTypes.BellatrixForkVersionMainnet
		capellaForkVersion = CapellaForkVersionMainnet
	case EthNetworkZhejiang:
		genesisForkVersion = GenesisForkVersionZhejiang
		genesisValidatorsRoot = GenesisValidatorsRootZhejiang
		bellatrixForkVersion = BellatrixForkVersionZhejiang
		capellaForkVersion = CapellaForkVersionZhejiang
	case EthNetworkCustom:
		genesisForkVersion = os.Getenv("GENESIS_FORK_VERSION")
		genesisValidatorsRoot = os.Getenv("GENESIS_VALIDATORS_ROOT")
		bellatrixForkVersion = os.Getenv("BELLATRIX_FORK_VERSION")
		capellaForkVersion = os.Getenv("CAPELLA_FORK_VERSION")
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownNetwork, networkName)
	}

	domainBuilder, err = ComputeDomain(boostTypes.DomainTypeAppBuilder, genesisForkVersion, boostTypes.Root{}.String())
	if err != nil {
		return nil, err
	}

	domainBeaconProposerBellatrix, err = ComputeDomain(boostTypes.DomainTypeBeaconProposer, bellatrixForkVersion, genesisValidatorsRoot)
	if err != nil {
		return nil, err
	}

	domainBeaconProposerCapella, err = ComputeDomain(boostTypes.DomainTypeBeaconProposer, capellaForkVersion, genesisValidatorsRoot)
	if err != nil {
		return nil, err
	}

	return &EthNetworkDetails{
		Name:                          networkName,
		GenesisForkVersionHex:         genesisForkVersion,
		GenesisValidatorsRootHex:      genesisValidatorsRoot,
		BellatrixForkVersionHex:       bellatrixForkVersion,
		CapellaForkVersionHex:         capellaForkVersion,
		DomainBuilder:                 domainBuilder,
		DomainBeaconProposerBellatrix: domainBeaconProposerBellatrix,
		DomainBeaconProposerCapella:   domainBeaconProposerCapella,
	}, nil
}

func (e *EthNetworkDetails) String() string {
	return fmt.Sprintf("EthNetworkDetails{Name: %s, GenesisForkVersionHex: %s, GenesisValidatorsRootHex: %s, BellatrixForkVersionHex: %s, CapellaForkVersionHex: %s, DomainBuilder: %x, DomainBeaconProposerBellatrix: %x, DomainBeaconProposerCapella: %x}",
		e.Name, e.GenesisForkVersionHex, e.GenesisValidatorsRootHex, e.BellatrixForkVersionHex, e.CapellaForkVersionHex, e.DomainBuilder, e.DomainBeaconProposerBellatrix, e.DomainBeaconProposerCapella)
}

type BuilderGetValidatorsResponseEntry struct {
	Slot           uint64                                  `json:"slot,string"`
	ValidatorIndex uint64                                  `json:"validator_index,string"`
	Entry          *boostTypes.SignedValidatorRegistration `json:"entry"`
}

type BidTraceV2 struct {
	apiv1.BidTrace
	BlockNumber uint64 `json:"block_number,string" db:"block_number"`
	NumTx       uint64 `json:"num_tx,string" db:"num_tx"`
}

type BidTraceV2JSON struct {
	Slot                 uint64 `json:"slot,string"`
	ParentHash           string `json:"parent_hash"`
	BlockHash            string `json:"block_hash"`
	BuilderPubkey        string `json:"builder_pubkey"`
	ProposerPubkey       string `json:"proposer_pubkey"`
	ProposerFeeRecipient string `json:"proposer_fee_recipient"`
	GasLimit             uint64 `json:"gas_limit,string"`
	GasUsed              uint64 `json:"gas_used,string"`
	Value                string `json:"value"`
	NumTx                uint64 `json:"num_tx,string"`
	BlockNumber          uint64 `json:"block_number,string"`
}

func (b BidTraceV2) MarshalJSON() ([]byte, error) {
	return json.Marshal(&BidTraceV2JSON{
		Slot:                 b.Slot,
		ParentHash:           b.ParentHash.String(),
		BlockHash:            b.BlockHash.String(),
		BuilderPubkey:        b.BuilderPubkey.String(),
		ProposerPubkey:       b.ProposerPubkey.String(),
		ProposerFeeRecipient: b.ProposerFeeRecipient.String(),
		GasLimit:             b.GasLimit,
		GasUsed:              b.GasUsed,
		Value:                b.Value.ToBig().String(),
		NumTx:                b.NumTx,
		BlockNumber:          b.BlockNumber,
	})
}

func (b *BidTraceV2) UnmarshalJSON(data []byte) error {
	params := &struct {
		NumTx       uint64 `json:"num_tx,string"`
		BlockNumber uint64 `json:"block_number,string"`
	}{}
	err := json.Unmarshal(data, params)
	if err != nil {
		return err
	}
	b.NumTx = params.NumTx
	b.BlockNumber = params.BlockNumber

	bidTrace := new(apiv1.BidTrace)
	err = json.Unmarshal(data, bidTrace)
	if err != nil {
		return err
	}
	b.BidTrace = *bidTrace
	return nil
}

func (b *BidTraceV2JSON) CSVHeader() []string {
	return []string{
		"slot",
		"parent_hash",
		"block_hash",
		"builder_pubkey",
		"proposer_pubkey",
		"proposer_fee_recipient",
		"gas_limit",
		"gas_used",
		"value",
		"num_tx",
		"block_number",
	}
}

func (b *BidTraceV2JSON) ToCSVRecord() []string {
	return []string{
		fmt.Sprint(b.Slot),
		b.ParentHash,
		b.BlockHash,
		b.BuilderPubkey,
		b.ProposerPubkey,
		b.ProposerFeeRecipient,
		fmt.Sprint(b.GasLimit),
		fmt.Sprint(b.GasUsed),
		b.Value,
		fmt.Sprint(b.NumTx),
		fmt.Sprint(b.BlockNumber),
	}
}

type BidTraceV2WithTimestampJSON struct {
	BidTraceV2JSON
	Timestamp            int64 `json:"timestamp,string,omitempty"`
	TimestampMs          int64 `json:"timestamp_ms,string,omitempty"`
	OptimisticSubmission bool  `json:"optimistic_submission"`
}

func (b *BidTraceV2WithTimestampJSON) CSVHeader() []string {
	return []string{
		"slot",
		"parent_hash",
		"block_hash",
		"builder_pubkey",
		"proposer_pubkey",
		"proposer_fee_recipient",
		"gas_limit",
		"gas_used",
		"value",
		"num_tx",
		"block_number",
		"timestamp",
		"timestamp_ms",
		"optimistic_submission",
	}
}

func (b *BidTraceV2WithTimestampJSON) ToCSVRecord() []string {
	return []string{
		fmt.Sprint(b.Slot),
		b.ParentHash,
		b.BlockHash,
		b.BuilderPubkey,
		b.ProposerPubkey,
		b.ProposerFeeRecipient,
		fmt.Sprint(b.GasLimit),
		fmt.Sprint(b.GasUsed),
		b.Value,
		fmt.Sprint(b.NumTx),
		fmt.Sprint(b.BlockNumber),
		fmt.Sprint(b.Timestamp),
		fmt.Sprint(b.TimestampMs),
		fmt.Sprint(b.OptimisticSubmission),
	}
}

type SignedBlindedBeaconBlock struct {
	Bellatrix *boostTypes.SignedBlindedBeaconBlock
	Capella   *apiv1capella.SignedBlindedBeaconBlock
}

func (s *SignedBlindedBeaconBlock) MarshalJSON() ([]byte, error) {
	if s.Capella != nil {
		return json.Marshal(s.Capella)
	}
	if s.Bellatrix != nil {
		return json.Marshal(s.Bellatrix)
	}
	return nil, ErrEmptyPayload
}

func (s *SignedBlindedBeaconBlock) Slot() uint64 {
	if s.Capella != nil {
		return uint64(s.Capella.Message.Slot)
	}
	if s.Bellatrix != nil {
		return s.Bellatrix.Message.Slot
	}
	return 0
}

func (s *SignedBlindedBeaconBlock) BlockHash() string {
	if s.Capella != nil {
		return s.Capella.Message.Body.ExecutionPayloadHeader.BlockHash.String()
	}
	if s.Bellatrix != nil {
		return s.Bellatrix.Message.Body.ExecutionPayloadHeader.BlockHash.String()
	}
	return ""
}

func (s *SignedBlindedBeaconBlock) BlockNumber() uint64 {
	if s.Capella != nil {
		return s.Capella.Message.Body.ExecutionPayloadHeader.BlockNumber
	}
	if s.Bellatrix != nil {
		return s.Bellatrix.Message.Body.ExecutionPayloadHeader.BlockNumber
	}
	return 0
}

func (s *SignedBlindedBeaconBlock) ProposerIndex() uint64 {
	if s.Capella != nil {
		return uint64(s.Capella.Message.ProposerIndex)
	}
	if s.Bellatrix != nil {
		return s.Bellatrix.Message.ProposerIndex
	}
	return 0
}

func (s *SignedBlindedBeaconBlock) Signature() []byte {
	if s.Capella != nil {
		return s.Capella.Signature[:]
	}
	if s.Bellatrix != nil {
		return s.Bellatrix.Signature[:]
	}
	return nil
}

//nolint:nolintlint,ireturn
func (s *SignedBlindedBeaconBlock) Message() boostTypes.HashTreeRoot {
	if s.Capella != nil {
		return s.Capella.Message
	}
	if s.Bellatrix != nil {
		return s.Bellatrix.Message
	}
	return nil
}

type SignedBeaconBlock struct {
	Capella *consensuscapella.SignedBeaconBlock
}

func (s *SignedBeaconBlock) MarshalJSON() ([]byte, error) {
	if s.Capella != nil {
		return json.Marshal(s.Capella)
	}
	return nil, ErrEmptyPayload
}

func (s *SignedBeaconBlock) Slot() uint64 {
	if s.Capella != nil {
		return uint64(s.Capella.Message.Slot)
	}
	return 0
}

func (s *SignedBeaconBlock) BlockHash() string {
	if s.Capella != nil {
		return s.Capella.Message.Body.ExecutionPayload.BlockHash.String()
	}
	return ""
}

type BuilderSubmitBlockRequest struct {
	Capella *capella.SubmitBlockRequest
}

func (b *BuilderSubmitBlockRequest) MarshalJSON() ([]byte, error) {
	if b.Capella != nil {
		return json.Marshal(b.Capella)
	}
	return nil, ErrEmptyPayload
}

func (b *BuilderSubmitBlockRequest) UnmarshalJSON(data []byte) error {
	capella := new(capella.SubmitBlockRequest)
	err := json.Unmarshal(data, capella)
	if err != nil {
		return err
	}
	b.Capella = capella
	return nil
}

func (b *BuilderSubmitBlockRequest) HasExecutionPayload() bool {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload != nil
	}
	return false
}

func (b *BuilderSubmitBlockRequest) ExecutionPayloadResponse() (*api.VersionedExecutionPayload, error) {
	if b.Capella != nil {
		return &api.VersionedExecutionPayload{ //nolint:exhaustruct
			Version: consensusspec.DataVersionCapella,
			Capella: b.Capella.ExecutionPayload,
		}, nil
	}

	return nil, ErrEmptyPayload
}

func (b *BuilderSubmitBlockRequest) Slot() uint64 {
	if b.Capella != nil {
		return b.Capella.Message.Slot
	}
	return 0
}

func (b *BuilderSubmitBlockRequest) BlockHash() string {
	if b.Capella != nil {
		return b.Capella.Message.BlockHash.String()
	}
	return ""
}

func (b *BuilderSubmitBlockRequest) ExecutionPayloadBlockHash() string {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload.BlockHash.String()
	}
	return ""
}

func (b *BuilderSubmitBlockRequest) BuilderPubkey() phase0.BLSPubKey {
	if b.Capella != nil {
		return b.Capella.Message.BuilderPubkey
	}
	return phase0.BLSPubKey{}
}

func (b *BuilderSubmitBlockRequest) ProposerFeeRecipient() string {
	if b.Capella != nil {
		return b.Capella.Message.ProposerFeeRecipient.String()
	}
	return ""
}

func (b *BuilderSubmitBlockRequest) Timestamp() uint64 {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload.Timestamp
	}
	return 0
}

func (b *BuilderSubmitBlockRequest) ProposerPubkey() string {
	if b.Capella != nil {
		return b.Capella.Message.ProposerPubkey.String()
	}
	return ""
}

func (b *BuilderSubmitBlockRequest) ParentHash() string {
	if b.Capella != nil {
		return b.Capella.Message.ParentHash.String()
	}
	return ""
}

func (b *BuilderSubmitBlockRequest) ExecutionPayloadParentHash() string {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload.ParentHash.String()
	}
	return ""
}

func (b *BuilderSubmitBlockRequest) Value() *big.Int {
	if b.Capella != nil {
		return b.Capella.Message.Value.ToBig()
	}
	return nil
}

func (b *BuilderSubmitBlockRequest) NumTx() int {
	if b.Capella != nil {
		return len(b.Capella.ExecutionPayload.Transactions)
	}
	return 0
}

func (b *BuilderSubmitBlockRequest) BlockNumber() uint64 {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload.BlockNumber
	}
	return 0
}

func (b *BuilderSubmitBlockRequest) GasUsed() uint64 {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload.GasUsed
	}
	return 0
}

func (b *BuilderSubmitBlockRequest) GasLimit() uint64 {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload.GasLimit
	}
	return 0
}

func (b *BuilderSubmitBlockRequest) Signature() phase0.BLSSignature {
	if b.Capella != nil {
		return b.Capella.Signature
	}
	return phase0.BLSSignature{}
}

func (b *BuilderSubmitBlockRequest) Random() string {
	if b.Capella != nil {
		return fmt.Sprintf("%#x", b.Capella.ExecutionPayload.PrevRandao)
	}
	return ""
}

func (b *BuilderSubmitBlockRequest) Message() *apiv1.BidTrace {
	if b.Capella != nil {
		return b.Capella.Message
	}
	return nil
}

type GetHeaderResponse struct {
	Bellatrix *boostTypes.GetHeaderResponse
	Capella   *spec.VersionedSignedBuilderBid
}

func (p *GetHeaderResponse) UnmarshalJSON(data []byte) error {
	capella := new(spec.VersionedSignedBuilderBid)
	err := json.Unmarshal(data, capella)
	if err == nil && capella.Capella != nil {
		p.Capella = capella
		return nil
	}
	bellatrix := new(boostTypes.GetHeaderResponse)
	err = json.Unmarshal(data, bellatrix)
	if err != nil {
		return err
	}
	p.Bellatrix = bellatrix
	return nil
}

func (p *GetHeaderResponse) MarshalJSON() ([]byte, error) {
	if p.Capella != nil {
		return json.Marshal(p.Capella)
	}
	if p.Bellatrix != nil {
		return json.Marshal(p.Bellatrix)
	}
	return nil, ErrEmptyPayload
}

func (p *GetHeaderResponse) Value() *big.Int {
	if p.Capella != nil {
		return p.Capella.Capella.Message.Value.ToBig()
	}
	if p.Bellatrix != nil {
		return p.Bellatrix.Data.Message.Value.BigInt()
	}
	return nil
}

func (p *GetHeaderResponse) BlockHash() phase0.Hash32 {
	if p.Capella != nil {
		return p.Capella.Capella.Message.Header.BlockHash
	}
	if p.Bellatrix != nil {
		return phase0.Hash32(p.Bellatrix.Data.Message.Header.BlockHash)
	}
	return phase0.Hash32{}
}

func (p *GetHeaderResponse) Empty() bool {
	if p == nil {
		return true
	}
	if p.Capella != nil {
		return p.Capella.Capella == nil || p.Capella.Capella.Message == nil
	}
	if p.Bellatrix != nil {
		return p.Bellatrix.Data == nil || p.Bellatrix.Data.Message == nil
	}
	return true
}

func (b *BuilderSubmitBlockRequest) Withdrawals() []*consensuscapella.Withdrawal {
	if b.Capella != nil {
		return b.Capella.ExecutionPayload.Withdrawals
	}
	return nil
}
