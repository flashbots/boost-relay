package api

import (
	"errors"

	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/mev-boost-relay/common"
)

var (
	ErrBlockHashMismatch  = errors.New("blockHash mismatch")
	ErrParentHashMismatch = errors.New("parentHash mismatch")
)

func SanityCheckBuilderBlockSubmission(payload *common.BuilderSubmitBlockRequest) error {
	if payload.BlockHash() != payload.ExecutionPayloadBlockHash() {
		return ErrBlockHashMismatch
	}

	if payload.ParentHash() != payload.ExecutionPayloadParentHash() {
		return ErrParentHashMismatch
	}

	return nil
}

func checkBLSPublicKeyHex(pkHex string) error {
	var proposerPubkey types.PublicKey
	return proposerPubkey.UnmarshalText([]byte(pkHex))
}
