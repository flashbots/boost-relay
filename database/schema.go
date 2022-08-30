package database

import (
	"github.com/flashbots/mev-boost-relay/common"
)

var (
	tableBase = common.GetEnv("DB_TABLE_PREFIX", "dev")

	TableValidatorRegistration  = tableBase + "_validator_registration"
	TableExecutionPayload       = tableBase + "_execution_payload"
	TableBuilderBlockSubmission = tableBase + "_builder_block_submission"
	TableDeliveredPayload       = tableBase + "_payload_delivered"
)

var schema = `
CREATE TABLE IF NOT EXISTS ` + TableValidatorRegistration + ` (
	id          bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
	inserted_at timestamp NOT NULL default current_timestamp,

	pubkey        varchar(98) NOT NULL UNIQUE,
	fee_recipient varchar(42) NOT NULL,
	timestamp     bigint NOT NULL,
	gas_limit     bigint NOT NULL,
	signature     text NOT NULL
);


CREATE TABLE IF NOT EXISTS ` + TableExecutionPayload + ` (
	id          bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
	inserted_at timestamp NOT NULL default current_timestamp,

	slot            bigint NOT NULL,
	proposer_pubkey varchar(98) NOT NULL,
	block_hash      varchar(66) NOT NULL,

	version     text NOT NULL, -- bellatrix
	payload 	json NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS ` + TableExecutionPayload + `_slot_pk_hash_idx ON ` + TableExecutionPayload + `(slot, proposer_pubkey, block_hash);


CREATE TABLE IF NOT EXISTS ` + TableBuilderBlockSubmission + ` (
	id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
	inserted_at timestamp NOT NULL default current_timestamp,

	execution_payload_id bigint references ` + TableExecutionPayload + `(id) on delete set null,

	-- simulation & verification results
	sim_success boolean NOT NULL,
	sim_error   text    NOT NULL,

	-- bidtrace data
	signature            text NOT NULL,

	slot        bigint NOT NULL,
	parent_hash varchar(66) NOT NULL,
	block_hash  varchar(66) NOT NULL,

	builder_pubkey         varchar(98) NOT NULL,
	proposer_pubkey        varchar(98) NOT NULL,
	proposer_fee_recipient varchar(42) NOT NULL,

	gas_used   bigint NOT NULL,
	gas_limit  bigint NOT NULL,

	num_tx int NOT NULL,
	value  NUMERIC(48, 0),

	-- helpers
	epoch        bigint NOT NULL,
	block_number bigint NOT NULL
);

CREATE INDEX IF NOT EXISTS ` + TableBuilderBlockSubmission + `_slot_idx ON ` + TableBuilderBlockSubmission + `("slot");
CREATE INDEX IF NOT EXISTS ` + TableBuilderBlockSubmission + `_blockhash_idx ON ` + TableBuilderBlockSubmission + `("block_hash");
CREATE INDEX IF NOT EXISTS ` + TableBuilderBlockSubmission + `_blocknumber_idx ON ` + TableBuilderBlockSubmission + `("block_number");
CREATE INDEX IF NOT EXISTS ` + TableBuilderBlockSubmission + `_simsuccess_idx ON ` + TableBuilderBlockSubmission + `("sim_success");


CREATE TABLE IF NOT EXISTS ` + TableDeliveredPayload + ` (
	id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
	inserted_at timestamp NOT NULL default current_timestamp,

	execution_payload_id        bigint references ` + TableExecutionPayload + `(id) on delete set null,
	signed_blinded_beacon_block json,

	epoch bigint NOT NULL,
	slot  bigint NOT NULL,

	builder_pubkey         varchar(98) NOT NULL,
	proposer_pubkey        varchar(98) NOT NULL,
	proposer_fee_recipient varchar(42) NOT NULL,

	parent_hash  varchar(66) NOT NULL,
	block_hash   varchar(66) NOT NULL,
	block_number bigint NOT NULL,

	gas_used  bigint NOT NULL,
	gas_limit bigint NOT NULL,

	num_tx  int NOT NULL,
	value   NUMERIC(48, 0),

	UNIQUE (slot, proposer_pubkey, block_hash)
);

CREATE INDEX IF NOT EXISTS ` + TableDeliveredPayload + `_slot_idx ON ` + TableDeliveredPayload + `("slot");
CREATE INDEX IF NOT EXISTS ` + TableDeliveredPayload + `_blockhash_idx ON ` + TableDeliveredPayload + `("block_hash");
CREATE INDEX IF NOT EXISTS ` + TableDeliveredPayload + `_blocknumber_idx ON ` + TableDeliveredPayload + `("block_number");
`
