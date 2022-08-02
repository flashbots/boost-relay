package database

import "github.com/flashbots/go-boost-utils/types"

type MockDB struct {
}

func (db MockDB) SaveValidatorRegistration(registration types.SignedValidatorRegistration) error {
	return nil
}

func (db MockDB) SaveDeliveredPayload(entry *DeliveredPayloadEntry) error {
	return nil
}

func (db MockDB) GetRecentDeliveredPayloads(filters GetPayloadsFilters) ([]*DeliveredPayloadEntry, error) {
	return nil, nil
}

func (db MockDB) SaveBuilderBlockSubmission(entry *BuilderBlockEntry) (int64, error) {
	return 0, nil
}
