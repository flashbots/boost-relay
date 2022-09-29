package datastore

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/mev-boost-relay/common"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) *RedisCache {
	t.Helper()
	var err error

	redisTestServer, err := miniredis.Run()
	require.NoError(t, err)

	redisService, err := NewRedisCache(redisTestServer.Addr(), "")
	require.NoError(t, err)

	return redisService
}

func TestRedisValidatorRegistration(t *testing.T) {
	cache := setupTestRedis(t)

	t.Run("Can save and get validator registration from cache", func(t *testing.T) {
		key := common.ValidPayloadRegisterValidator.Message.Pubkey
		value := common.ValidPayloadRegisterValidator
		pkHex := types.NewPubkeyHex(key.String())
		err := cache.SetValidatorRegistrationTimestamp(pkHex, value.Message.Timestamp)
		require.NoError(t, err)
		result, err := cache.GetValidatorRegistrationTimestamp(key.PubkeyHex())
		require.NoError(t, err)
		require.Equal(t, result, value.Message.Timestamp)
	})

	t.Run("Returns nil if validator registration is not in cache", func(t *testing.T) {
		key := types.PublicKey{}
		result, err := cache.GetValidatorRegistrationTimestamp(key.PubkeyHex())
		require.NoError(t, err)
		require.Equal(t, uint64(0), result)
	})

	t.Run("test SetValidatorRegistrationTimestampIfNewer", func(t *testing.T) {
		key := common.ValidPayloadRegisterValidator.Message.Pubkey
		value := common.ValidPayloadRegisterValidator

		pkHex := types.NewPubkeyHex(key.String())
		timestamp := value.Message.Timestamp

		err := cache.SetValidatorRegistrationTimestampIfNewer(pkHex, timestamp)
		require.NoError(t, err)

		result, err := cache.GetValidatorRegistrationTimestamp(key.PubkeyHex())
		require.NoError(t, err)
		require.Equal(t, result, timestamp)

		// Try to set an older timestamp (should not work)
		timestamp2 := timestamp - 10
		err = cache.SetValidatorRegistrationTimestampIfNewer(pkHex, timestamp2)
		require.NoError(t, err)
		result, err = cache.GetValidatorRegistrationTimestamp(key.PubkeyHex())
		require.NoError(t, err)
		require.Equal(t, result, timestamp)

		// Try to set an older timestamp (should not work)
		timestamp3 := timestamp + 10
		err = cache.SetValidatorRegistrationTimestampIfNewer(pkHex, timestamp3)
		require.NoError(t, err)
		result, err = cache.GetValidatorRegistrationTimestamp(key.PubkeyHex())
		require.NoError(t, err)
		require.Equal(t, result, timestamp3)
	})
}

func TestRedisKnownValidators(t *testing.T) {
	cache := setupTestRedis(t)

	t.Run("Can save and get known validators", func(t *testing.T) {
		key1 := types.NewPubkeyHex("0x1a1d7b8dd64e0aafe7ea7b6c95065c9364cf99d38470c12ee807d55f7de1529ad29ce2c422e0b65e3d5a05c02caca249")
		key2 := types.NewPubkeyHex("0x2a1d7b8dd64e0aafe7ea7b6c95065c9364cf99d38470c12ee807d55f7de1529ad29ce2c422e0b65e3d5a05c02caca249")
		require.NoError(t, cache.SetKnownValidator(key1, 1))
		require.NoError(t, cache.SetKnownValidator(key2, 2))

		knownVals, err := cache.GetKnownValidators()
		require.NoError(t, err)
		require.Equal(t, 2, len(knownVals))
		require.Contains(t, knownVals, key1)
		require.Contains(t, knownVals, key2)
	})
}

func TestRedisValidatorRegistrations(t *testing.T) {
	cache := setupTestRedis(t)

	t.Run("Can save and get validator registrations", func(t *testing.T) {
		key1 := types.NewPubkeyHex("0x1a1d7b8dd64e0aafe7ea7b6c95065c9364cf99d38470c12ee807d55f7de1529ad29ce2c422e0b65e3d5a05c02caca249")
		require.NoError(t, cache.SetKnownValidator(key1, 1))

		knownVals, err := cache.GetKnownValidators()
		require.NoError(t, err)
		require.Equal(t, 1, len(knownVals))
		require.Contains(t, knownVals, key1)

		// Create a signed registration for key1
		pubkey1, err := types.HexToPubkey(key1.String())
		require.NoError(t, err)
		entry := types.SignedValidatorRegistration{
			Message: &types.RegisterValidatorRequestMessage{
				FeeRecipient: types.Address{0x02},
				GasLimit:     5000,
				Timestamp:    0xffffffff,
				Pubkey:       pubkey1,
			},
			Signature: types.Signature{},
		}

		pkHex := types.NewPubkeyHex(entry.Message.Pubkey.String())
		err = cache.SetValidatorRegistrationTimestamp(pkHex, entry.Message.Timestamp)
		require.NoError(t, err)

		reg, err := cache.GetValidatorRegistrationTimestamp(key1)
		require.NoError(t, err)
		require.Equal(t, uint64(0xffffffff), reg)
	})
}

func TestRedisProposerDuties(t *testing.T) {
	cache := setupTestRedis(t)
	duties := []types.BuilderGetValidatorsResponseEntry{
		{
			Slot: 1,
			Entry: &types.SignedValidatorRegistration{
				Signature: types.Signature{},
				Message: &types.RegisterValidatorRequestMessage{
					FeeRecipient: types.Address{0x02},
					GasLimit:     5000,
					Timestamp:    0xffffffff,
					Pubkey:       types.PublicKey{},
				},
			},
		},
	}
	err := cache.SetProposerDuties(duties)
	require.NoError(t, err)

	duties2, err := cache.GetProposerDuties()
	require.NoError(t, err)

	require.Equal(t, 1, len(duties2))
	require.Equal(t, duties[0].Entry.Message.FeeRecipient, duties2[0].Entry.Message.FeeRecipient)
}

func TestActiveValidators(t *testing.T) {
	pk1 := types.NewPubkeyHex("0x8016d3229030424cfeff6c5b813970ea193f8d012cfa767270ca9057d58eddc556e96c14544bf4c038dbed5f24aa8da0")
	cache := setupTestRedis(t)
	err := cache.SetActiveValidator(pk1)
	require.NoError(t, err)

	vals, err := cache.GetActiveValidators()
	require.NoError(t, err)
	require.Equal(t, 1, len(vals))
	require.True(t, vals[pk1])
}
