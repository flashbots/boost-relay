// Package api contains the API webserver for the proposer and block-builder APIs
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flashbots/go-boost-utils/bls"
	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/go-utils/httplogger"
	"github.com/flashbots/mev-boost-relay/beaconclient"
	"github.com/flashbots/mev-boost-relay/common"
	"github.com/flashbots/mev-boost-relay/database"
	"github.com/flashbots/mev-boost-relay/datastore"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	uberatomic "go.uber.org/atomic"
)

var (
	ErrMissingLogOpt              = errors.New("log parameter is nil")
	ErrMissingBeaconClientOpt     = errors.New("beacon-client is nil")
	ErrMissingDatastoreOpt        = errors.New("proposer datastore is nil")
	ErrRelayPubkeyMismatch        = errors.New("relay pubkey does not match existing one")
	ErrServerAlreadyStarted       = errors.New("server was already started")
	ErrBuilderAPIWithoutSecretKey = errors.New("cannot start builder API without secret key")
)

var (
	// Proposer API (builder-specs)
	pathStatus            = "/eth/v1/builder/status"
	pathRegisterValidator = "/eth/v1/builder/validators"
	pathGetHeader         = "/eth/v1/builder/header/{slot:[0-9]+}/{parent_hash:0x[a-fA-F0-9]+}/{pubkey:0x[a-fA-F0-9]+}"
	pathGetPayload        = "/eth/v1/builder/blinded_blocks"

	// Block builder API
	pathBuilderGetValidators = "/relay/v1/builder/validators"
	pathSubmitNewBlock       = "/relay/v1/builder/blocks"

	// Data API
	pathDataProposerPayloadDelivered = "/relay/v1/data/bidtraces/proposer_payload_delivered"
	pathDataBuilderBidsReceived      = "/relay/v1/data/bidtraces/builder_blocks_received"
	pathDataValidatorRegistration    = "/relay/v1/data/validator_registration"

	// Internal API
	pathInternalBuilderStatus = "/internal/v1/builder/{pubkey:0x[a-fA-F0-9]+}"
)

// RelayAPIOpts contains the options for a relay
type RelayAPIOpts struct {
	Log *logrus.Entry

	ListenAddr  string
	BlockSimURL string

	BeaconClient beaconclient.IMultiBeaconClient
	Datastore    *datastore.Datastore
	Redis        *datastore.RedisCache
	DB           database.IDatabaseService

	SecretKey *bls.SecretKey // used to sign bids (getHeader responses)

	// Network specific variables
	EthNetDetails common.EthNetworkDetails

	// APIs to enable
	ProposerAPI     bool
	BlockBuilderAPI bool
	DataAPI         bool
	PprofAPI        bool
	InternalAPI     bool
}

// RelayAPI represents a single Relay instance
type RelayAPI struct {
	opts RelayAPIOpts
	log  *logrus.Entry

	blsSk     *bls.SecretKey
	publicKey *types.PublicKey

	srv        *http.Server
	srvStarted uberatomic.Bool

	beaconClient beaconclient.IMultiBeaconClient
	datastore    *datastore.Datastore
	redis        *datastore.RedisCache
	db           database.IDatabaseService

	headSlot uberatomic.Uint64

	proposerDutiesLock       sync.RWMutex
	proposerDutiesResponse   []types.BuilderGetValidatorsResponseEntry
	proposerDutiesSlot       uint64
	isUpdatingProposerDuties uberatomic.Bool

	blockSimRateLimiter *BlockSimulationRateLimiter

	// Feature flags
	ffForceGetHeader204      bool
	ffDisableBlockPublishing bool
	ffDisableLowPrioBuilders bool
}

// NewRelayAPI creates a new service. if builders is nil, allow any builder
func NewRelayAPI(opts RelayAPIOpts) (*RelayAPI, error) {
	if opts.Log == nil {
		return nil, ErrMissingLogOpt
	}

	if opts.BeaconClient == nil {
		return nil, ErrMissingBeaconClientOpt
	}

	if opts.Datastore == nil {
		return nil, ErrMissingDatastoreOpt
	}

	// If block-builder API is enabled, then ensure secret key is all set
	var publicKey types.PublicKey
	if opts.BlockBuilderAPI {
		if opts.SecretKey == nil {
			return nil, ErrBuilderAPIWithoutSecretKey
		}

		// If using a secret key, ensure it's the correct one
		publicKey = types.BlsPublicKeyToPublicKey(bls.PublicKeyFromSecretKey(opts.SecretKey))
		opts.Log.Infof("Using BLS key: %s", publicKey.String())

		// ensure pubkey is same across all relay instances
		_pubkey, err := opts.Redis.GetRelayConfig(datastore.RedisConfigFieldPubkey)
		if err != nil {
			return nil, err
		} else if _pubkey == "" {
			err := opts.Redis.SetRelayConfig(datastore.RedisConfigFieldPubkey, publicKey.String())
			if err != nil {
				return nil, err
			}
		} else if _pubkey != publicKey.String() {
			return nil, fmt.Errorf("%w: new=%s old=%s", ErrRelayPubkeyMismatch, publicKey.String(), _pubkey)
		}
	}

	api := RelayAPI{
		opts:                   opts,
		log:                    opts.Log,
		blsSk:                  opts.SecretKey,
		publicKey:              &publicKey,
		datastore:              opts.Datastore,
		beaconClient:           opts.BeaconClient,
		redis:                  opts.Redis,
		db:                     opts.DB,
		proposerDutiesResponse: []types.BuilderGetValidatorsResponseEntry{},
		blockSimRateLimiter:    NewBlockSimulationRateLimiter(opts.BlockSimURL),
	}

	if os.Getenv("FORCE_GET_HEADER_204") == "1" {
		api.log.Warn("env: FORCE_GET_HEADER_204 - forcing getHeader to always return 204")
		api.ffForceGetHeader204 = true
	}

	if os.Getenv("DISABLE_BLOCK_PUBLISHING") == "1" {
		api.log.Warn("env: DISABLE_BLOCK_PUBLISHING - disabling publishing blocks on getPayload")
		api.ffDisableBlockPublishing = true
	}

	if os.Getenv("DISABLE_LOWPRIO_BUILDERS") == "1" {
		api.log.Warn("env: DISABLE_LOWPRIO_BUILDERS - allowing only high-level builders")
		api.ffDisableLowPrioBuilders = true
	}

	return &api, nil
}

func (api *RelayAPI) getRouter() http.Handler {
	r := mux.NewRouter()

	r.HandleFunc("/", api.handleRoot).Methods(http.MethodGet)

	// Proposer API
	if api.opts.ProposerAPI {
		api.log.Info("proposer API enabled")
		r.HandleFunc(pathStatus, api.handleStatus).Methods(http.MethodGet)
		r.HandleFunc(pathRegisterValidator, api.handleRegisterValidator).Methods(http.MethodPost)
		r.HandleFunc(pathGetHeader, api.handleGetHeader).Methods(http.MethodGet)
		r.HandleFunc(pathGetPayload, api.handleGetPayload).Methods(http.MethodPost)
	}

	// Builder API
	if api.opts.BlockBuilderAPI {
		api.log.Info("block builder API enabled")
		r.HandleFunc(pathBuilderGetValidators, api.handleBuilderGetValidators).Methods(http.MethodGet)
		r.HandleFunc(pathSubmitNewBlock, api.handleSubmitNewBlock).Methods(http.MethodPost)
	}

	// Data API
	if api.opts.DataAPI {
		api.log.Info("data API enabled")
		r.HandleFunc(pathDataProposerPayloadDelivered, api.handleDataProposerPayloadDelivered).Methods(http.MethodGet)
		r.HandleFunc(pathDataBuilderBidsReceived, api.handleDataBuilderBidsReceived).Methods(http.MethodGet)
		r.HandleFunc(pathDataValidatorRegistration, api.handleDataValidatorRegistration).Methods(http.MethodGet)
	}

	// Pprof
	if api.opts.PprofAPI {
		api.log.Info("pprof API enabled")
		r.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)
	}

	// /internal/...
	if api.opts.InternalAPI {
		api.log.Info("internal API enabled")
		r.HandleFunc(pathInternalBuilderStatus, api.handleInternalBuilderStatus).Methods(http.MethodGet, http.MethodPost, http.MethodPut)
	}

	// r.Use(mux.CORSMethodMiddleware(r))
	loggedRouter := httplogger.LoggingMiddlewareLogrus(api.log, r)
	return loggedRouter
}

// StartServer starts the HTTP server for this instance
func (api *RelayAPI) StartServer() (err error) {
	if api.srvStarted.Swap(true) {
		return ErrServerAlreadyStarted
	}

	// Get best beacon-node status by head slot, process current slot and start slot updates
	bestSyncStatus, err := api.beaconClient.BestSyncStatus()
	if err != nil {
		return err
	}

	// Get current proposer duties
	api.updateProposerDuties(bestSyncStatus.HeadSlot)

	// Update list of known validators, and start refresh loop
	go api.startKnownValidatorUpdates()

	// Process current slot
	api.processNewSlot(bestSyncStatus.HeadSlot)

	// Start regular slot updates
	go func() {
		c := make(chan beaconclient.HeadEventData)
		api.beaconClient.SubscribeToHeadEvents(c)
		for {
			headEvent := <-c
			api.processNewSlot(headEvent.Slot)
		}
	}()

	// Periodically remove expired headers
	go func() {
		for {
			time.Sleep(2 * time.Minute)
			numRemoved, numRemaining := api.datastore.CleanupOldBidsAndBlocks(api.headSlot.Load())
			api.log.Infof("Removed %d old bids and blocks. Remaining: %d", numRemoved, numRemaining)
		}
	}()

	api.srv = &http.Server{
		Addr:    api.opts.ListenAddr,
		Handler: api.getRouter(),

		ReadTimeout:       600 * time.Millisecond,
		ReadHeaderTimeout: 400 * time.Millisecond,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       3 * time.Second,
	}

	err = api.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (api *RelayAPI) processNewSlot(headSlot uint64) {
	_apiHeadSlot := api.headSlot.Load()
	if headSlot <= _apiHeadSlot {
		return
	}

	if _apiHeadSlot > 0 {
		for s := _apiHeadSlot + 1; s < headSlot; s++ {
			api.log.WithField("missedSlot", s).Warnf("missed slot: %d", s)
		}
	}

	api.headSlot.Store(headSlot)
	epoch := headSlot / uint64(common.SlotsPerEpoch)
	api.log.WithFields(logrus.Fields{
		"epoch":              epoch,
		"slotHead":           headSlot,
		"slotStartNextEpoch": (epoch + 1) * uint64(common.SlotsPerEpoch),
	}).Infof("updated headSlot to %d", headSlot)

	// Regularly update proposer duties in the background
	go api.updateProposerDuties(headSlot)
}

func (api *RelayAPI) updateProposerDuties(headSlot uint64) {
	// Ensure only one updating is running at a time
	if api.isUpdatingProposerDuties.Swap(true) {
		return
	}
	defer api.isUpdatingProposerDuties.Store(false)

	// Update once every 8 slots (or more, if a slot was missed)
	if headSlot%8 != 0 && headSlot-api.proposerDutiesSlot < 8 {
		return
	}

	// Get duties from mem
	duties, err := api.redis.GetProposerDuties()

	if err == nil {
		api.proposerDutiesLock.Lock()
		api.proposerDutiesResponse = duties
		api.proposerDutiesSlot = headSlot
		api.proposerDutiesLock.Unlock()

		// pretty-print
		_duties := make([]string, len(duties))
		for i, duty := range duties {
			_duties[i] = fmt.Sprint(duty.Slot)
		}
		sort.Strings(_duties)
		api.log.Infof("proposer duties updated: %s", strings.Join(_duties, ", "))
	} else {
		api.log.WithError(err).Error("failed to update proposer duties")
	}
}

func (api *RelayAPI) startKnownValidatorUpdates() {
	for {
		// Refresh known validators
		cnt, err := api.datastore.RefreshKnownValidators()
		if err != nil {
			api.log.WithError(err).Error("error getting known validators")
		} else {
			api.log.WithField("cnt", cnt).Info("updated known validators")
		}

		// Wait for one epoch (at the beginning, because initially the validators have already been queried)
		time.Sleep(common.DurationPerEpoch / 2)
	}
}

func (api *RelayAPI) RespondError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	resp := HTTPErrorResp{code, message}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.log.WithField("response", resp).WithError(err).Error("Couldn't write error response")
		http.Error(w, "", http.StatusInternalServerError)
	}
}

func (api *RelayAPI) RespondOK(w http.ResponseWriter, response any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		api.log.WithField("response", response).WithError(err).Error("Couldn't write OK response")
		http.Error(w, "", http.StatusInternalServerError)
	}
}

func (api *RelayAPI) handleStatus(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ---------------
//  PROPOSER APIS
// ---------------

func (api *RelayAPI) handleRoot(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "MEV-Boost Relay API")
}

func (api *RelayAPI) handleRegisterValidator(w http.ResponseWriter, req *http.Request) {
	ua := req.UserAgent()
	log := api.log.WithFields(logrus.Fields{
		"method":    "registerValidator",
		"ua":        ua,
		"mevBoostV": common.GetMevBoostVersionFromUserAgent(ua),
	})

	respondError := func(code int, msg string) {
		log.Warn("bad request: ", msg)
		api.RespondError(w, code, msg)
	}

	start := time.Now()
	registrationTimeUpperBound := start.Add(10 * time.Second)

	registrations := []types.SignedValidatorRegistration{}
	numRegNew := 0

	if err := json.NewDecoder(req.Body).Decode(&registrations); err != nil {
		respondError(http.StatusBadRequest, "failed to decode payload")
		return
	}

	// Possible optimisations:
	// - GetValidatorRegistrationTimestamp could keep a cache in memory for some time and check memory first before going to Redis
	// - Do multiple loops and filter down set of registrations, and batch checks for all registrations instead of locking for each individually:
	//   (1) sanity checks, (2) IsKnownValidator, (3) CheckTimestamp, (4) Batch SetValidatorRegistration
	for _, registration := range registrations {
		if registration.Message == nil {
			respondError(http.StatusBadRequest, "registration without message")
			return
		}

		pubkey := registration.Message.Pubkey.PubkeyHex()
		regLog := api.log.WithFields(logrus.Fields{
			"pubkey":       pubkey,
			"feeRecipient": registration.Message.FeeRecipient.String(),
		})

		registrationTime := time.Unix(int64(registration.Message.Timestamp), 0)
		if registrationTime.After(registrationTimeUpperBound) {
			respondError(http.StatusBadRequest, "timestamp too far in the future")
			return
		}

		// Check if actually a real validator
		isKnownValidator := api.datastore.IsKnownValidator(pubkey)
		if !isKnownValidator {
			respondError(http.StatusBadRequest, fmt.Sprintf("not a known validator: %s", pubkey))
			return
		}

		// Check for a previous registration timestamp
		prevTimestamp, err := api.datastore.GetValidatorRegistrationTimestamp(pubkey)
		if err != nil {
			regLog.WithError(err).Infof("error getting last registration timestamp")
		}

		// Do nothing if the registration is already the latest
		if prevTimestamp >= registration.Message.Timestamp {
			continue
		}

		// Send to workers for signature verification and saving
		numRegNew++

		// Verify the signature
		ok, err := types.VerifySignature(registration.Message, api.opts.EthNetDetails.DomainBuilder, registration.Message.Pubkey[:], registration.Signature[:])
		if err != nil {
			regLog.WithError(err).Error("error verifying registerValidator signature")
			continue
		} else if !ok {
			api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("failed to verify validator signature for %s", registration.Message.Pubkey.String()))
			return
		} else {
			// Save
			go func(reg types.SignedValidatorRegistration) {
				err := api.datastore.SetValidatorRegistration(reg)
				if err != nil {
					regLog.WithError(err).Error("Failed to set validator registration")
				}
			}(registration)
		}
	}

	log = log.WithFields(logrus.Fields{
		"numRegistrations":    len(registrations),
		"numRegistrationsNew": numRegNew,
		"timeNeededSec":       time.Since(start).Seconds(),
	})
	log.Info("validator registrations call processed")
	w.WriteHeader(http.StatusOK)
}

func (api *RelayAPI) handleGetHeader(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	slotStr := vars["slot"]
	parentHashHex := vars["parent_hash"]
	proposerPubkeyHex := vars["pubkey"]
	ua := req.UserAgent()
	log := api.log.WithFields(logrus.Fields{
		"method":     "getHeader",
		"slot":       slotStr,
		"parentHash": parentHashHex,
		"pubkey":     proposerPubkeyHex,
		"ua":         ua,
		"mevBoostV":  common.GetMevBoostVersionFromUserAgent(ua),
	})

	slot, err := strconv.ParseUint(slotStr, 10, 64)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, common.ErrInvalidSlot.Error())
		return
	}

	if len(proposerPubkeyHex) != 98 {
		api.RespondError(w, http.StatusBadRequest, common.ErrInvalidPubkey.Error())
		return
	}

	if len(parentHashHex) != 66 {
		api.RespondError(w, http.StatusBadRequest, common.ErrInvalidHash.Error())
		return
	}

	if slot < api.headSlot.Load() {
		api.RespondError(w, http.StatusBadRequest, "slot is too old")
		return
	}

	log.Debug("getHeader request received")

	if api.ffForceGetHeader204 {
		log.Info("forced getHeader 204 response")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	bid, err := api.datastore.GetGetHeaderResponse(slot, parentHashHex, proposerPubkeyHex)
	if err != nil {
		log.WithError(err).Error("could not get bid")
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if bid == nil || bid.Data == nil || bid.Data.Message == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Error on bid without value
	if bid.Data.Message.Value.Cmp(&ZeroU256) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	log.WithFields(logrus.Fields{
		"value":     bid.Data.Message.Value.String(),
		"blockHash": bid.Data.Message.Header.BlockHash.String(),
	}).Info("bid delivered")
	api.RespondOK(w, bid)
}

func (api *RelayAPI) handleGetPayload(w http.ResponseWriter, req *http.Request) {
	log := api.log.WithField("method", "getPayload")

	payload := new(types.SignedBlindedBeaconBlock)
	if err := json.NewDecoder(req.Body).Decode(payload); err != nil {
		log.Warn("getPayload request failed to decode")
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	slot := payload.Message.Slot
	blockHash := payload.Message.Body.ExecutionPayloadHeader.BlockHash
	ua := req.UserAgent()
	log = log.WithFields(logrus.Fields{
		"slot":      slot,
		"blockHash": blockHash.String(),
		"idArg":     req.URL.Query().Get("id"),
		"ua":        ua,
		"mevBoostV": common.GetMevBoostVersionFromUserAgent(ua),
	})

	log.Debug("getPayload request received")

	proposerPubkey, found := api.datastore.GetKnownValidatorPubkeyByIndex(payload.Message.ProposerIndex)
	if !found {
		log.Errorf("could not find proposer pubkey for index %d", payload.Message.ProposerIndex)
		api.RespondError(w, http.StatusBadRequest, "could not match proposer index to pubkey")
		return
	}

	log = log.WithField("pubkeyFromIndex", proposerPubkey)

	// Get the proposer pubkey based on the validator index from the payload
	pk, err := types.HexToPubkey(proposerPubkey.String())
	if err != nil {
		log.WithError(err).Warn("could not convert pubkey to types.PublicKey")
		api.RespondError(w, http.StatusBadRequest, "could not convert pubkey to types.PublicKey")
		return
	}

	// Verify the signature
	ok, err := types.VerifySignature(payload.Message, api.opts.EthNetDetails.DomainBeaconProposer, pk[:], payload.Signature[:])
	if !ok || err != nil {
		log.WithError(err).Warn("could not verify payload signature")
		api.RespondError(w, http.StatusBadRequest, "could not verify payload signature")
		return
	}

	// Get the response - from memory, Redis or DB
	// note that mev-boost might send getPayload for bids of other relays, thus this code wouldn't find anything
	getPayloadResp, err := api.datastore.GetGetPayloadResponse(slot, proposerPubkey.String(), blockHash.String())
	if err != nil {
		log.WithError(err).Error("failed getting execution payload from db")
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if getPayloadResp == nil {
		log.Info("failed getting execution payload")
		api.RespondError(w, http.StatusBadRequest, "no execution payload for this request")
		return
	}

	api.RespondOK(w, getPayloadResp)
	log = log.WithFields(logrus.Fields{
		"numTx":       len(getPayloadResp.Data.Transactions),
		"blockNumber": payload.Message.Body.ExecutionPayloadHeader.BlockNumber,
	})
	log.Info("execution payload delivered")

	// Save information about delivered payload
	go func() {
		err := api.db.SaveDeliveredPayload(slot, proposerPubkey, blockHash, payload)
		if err != nil {
			log.WithError(err).Error("failed to save delivered payload")
		}

		// Increment builder stats
		err = api.db.IncBlockBuilderStatsAfterGetPayload(slot, blockHash.String())
		if err != nil {
			log.WithError(err).Error("could not increment builder-stats after getHeader")
		}
	}()

	// Publish the signed beacon block via beacon-node
	go func() {
		if api.ffDisableBlockPublishing {
			log.Info("publishing the block is disabled")
			return
		}
		signedBeaconBlock := SignedBlindedBeaconBlockToBeaconBlock(payload, getPayloadResp.Data)
		_, err := api.beaconClient.PublishBlock(signedBeaconBlock)
		if err != nil {
			log.WithError(err).Error("failed to publish beacon block")
		}
	}()
}

// --------------------
//  BLOCK BUILDER APIS
// --------------------

func (api *RelayAPI) handleBuilderGetValidators(w http.ResponseWriter, req *http.Request) {
	api.proposerDutiesLock.RLock()
	defer api.proposerDutiesLock.RUnlock()
	api.RespondOK(w, api.proposerDutiesResponse)
}

func (api *RelayAPI) handleSubmitNewBlock(w http.ResponseWriter, req *http.Request) {
	log := api.log.WithField("method", "submitNewBlock")

	payload := new(types.BuilderSubmitBlockRequest)
	if err := json.NewDecoder(req.Body).Decode(payload); err != nil {
		log.WithError(err).Warn("could not decode payload")
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if payload.Message == nil || payload.ExecutionPayload == nil {
		api.RespondError(w, http.StatusBadRequest, "missing parts of the payload")
		return
	}

	log = log.WithFields(logrus.Fields{
		"slot":          payload.Message.Slot,
		"builderPubkey": payload.Message.BuilderPubkey.String(),
	})

	builderIsHighPrio, builderIsBlacklisted, err := api.redis.GetBlockBuilderStatus(payload.Message.BuilderPubkey.String())
	if err != nil {
		log.WithError(err).Error("could not get block builder status")
	}

	if builderIsBlacklisted {
		log.Info("builder is blacklisted")
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		return
	}

	// In case only high-prio requests are accepted, fail others
	if api.ffDisableLowPrioBuilders && !builderIsHighPrio {
		log.Info("rejecting low-prio builder (ff-disable-low-prio-builders)")
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		return
	}

	log = log.WithFields(logrus.Fields{
		"builderHighPrio": builderIsHighPrio,
		"proposerPubkey":  payload.Message.ProposerPubkey.String(),
		"blockHash":       payload.Message.BlockHash.String(),
		"parentHash":      payload.Message.ParentHash.String(),
		"value":           payload.Message.Value.String(),
		"tx":              len(payload.ExecutionPayload.Transactions),
	})

	if payload.Message.Slot <= api.headSlot.Load() {
		api.log.Debug("submitNewBlock failed: submission for past slot")
		api.RespondError(w, http.StatusBadRequest, "submission for past slot")
		return
	}

	// Don't accept blocks with 0 value
	if payload.Message.Value.Cmp(&ZeroU256) == 0 || len(payload.ExecutionPayload.Transactions) == 0 {
		api.log.Debug("submitNewBlock failed: block with 0 value or no txs")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Sanity check the submission
	err = VerifyBuilderBlockSubmission(payload)
	if err != nil {
		log.WithError(err).Warn("block submission sanity checks failed")
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Verify the signature
	ok, err := types.VerifySignature(payload.Message, api.opts.EthNetDetails.DomainBuilder, payload.Message.BuilderPubkey[:], payload.Signature[:])
	if !ok || err != nil {
		log.WithError(err).Warnf("could not verify builder signature")
		api.RespondError(w, http.StatusBadRequest, "invalid signature")
		return
	}

	var simErr error
	isMostProfitableBlock := false

	// At end of this function, save builder submission to database (in the background)
	defer func() {
		submissionEntry, err := api.db.SaveBuilderBlockSubmission(payload, simErr, isMostProfitableBlock)
		if err != nil {
			log.WithError(err).Error("saving builder block submission to database failed")
			return
		}

		err = api.db.UpsertBlockBuilderEntryAfterSubmission(submissionEntry, simErr != nil, isMostProfitableBlock)
		if err != nil {
			log.WithError(err).Error("failed to upsert block-builder-entry")
		}
	}()

	// Simulate the block submission and save to db
	t := time.Now()
	simErr = api.blockSimRateLimiter.send(req.Context(), payload, builderIsHighPrio)

	if simErr != nil {
		log.WithError(simErr).WithFields(logrus.Fields{
			"duration":   time.Since(t).Seconds(),
			"numWaiting": api.blockSimRateLimiter.currentCounter(),
		}).Warn("block validation failed")
		api.RespondError(w, http.StatusBadRequest, simErr.Error())
		return
	} else {
		log.WithFields(logrus.Fields{
			"duration":   time.Since(t).Seconds(),
			"numWaiting": api.blockSimRateLimiter.currentCounter(),
		}).Info("block validation successful")
	}

	// Check if there's already a bid
	prevBid, err := api.datastore.GetGetHeaderResponse(payload.Message.Slot, payload.Message.ParentHash.String(), payload.Message.ProposerPubkey.String())
	if err != nil {
		log.WithError(err).Error("error getting previous bid")
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Only proceed if this bid is higher than previous one
	isMostProfitableBlock = prevBid == nil || payload.Message.Value.Cmp(&prevBid.Data.Message.Value) == 1
	if !isMostProfitableBlock {
		log.Debug("block submission with same or lower value")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Prepare the response data
	signedBuilderBid, err := BuilderSubmitBlockRequestToSignedBuilderBid(payload, api.blsSk, api.publicKey, api.opts.EthNetDetails.DomainBuilder)
	if err != nil {
		log.WithError(err).Error("could not sign builder bid")
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	getHeaderResponse := types.GetHeaderResponse{
		Version: VersionBellatrix,
		Data:    signedBuilderBid,
	}

	getPayloadResponse := types.GetPayloadResponse{
		Version: VersionBellatrix,
		Data:    payload.ExecutionPayload,
	}

	signedBidTrace := types.SignedBidTrace{
		Message:   payload.Message,
		Signature: payload.Signature,
	}

	err = api.datastore.SaveBlockSubmission(&signedBidTrace, &getHeaderResponse, &getPayloadResponse)
	if err != nil {
		log.WithError(err).Error("could not save bid and block")
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.WithFields(logrus.Fields{
		"proposerPubkey":   payload.Message.ProposerPubkey.String(),
		"value":            payload.Message.Value.String(),
		"tx":               len(payload.ExecutionPayload.Transactions),
		"isMostProfitable": isMostProfitableBlock,
	}).Info("received block from builder")

	// Respond with OK (TODO: proper response response data type https://flashbots.notion.site/Relay-API-Spec-5fb0819366954962bc02e81cb33840f5#fa719683d4ae4a57bc3bf60e138b0dc6)
	w.WriteHeader(http.StatusOK)
}

// ---------------
//  INTERNAL APIS
// ---------------

func (api *RelayAPI) handleInternalBuilderStatus(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	builderPubkey := vars["pubkey"]

	if req.Method == http.MethodGet {
		builderEntry, err := api.db.GetBlockBuilderByPubkey(builderPubkey)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				api.RespondError(w, http.StatusBadRequest, "builder not found")
				return
			}

			api.log.WithError(err).Error("could not get block builder")
			api.RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}

		api.RespondOK(w, builderEntry)
		return
	} else if req.Method == http.MethodPost || req.Method == http.MethodPut || req.Method == http.MethodPatch {
		args := req.URL.Query()
		isHighPrio := args.Get("high_prio") == "true"
		isBlacklisted := args.Get("blacklisted") == "true"
		api.log.WithFields(logrus.Fields{
			"builderPubkey": builderPubkey,
			"isHighPrio":    isHighPrio,
			"isBlacklisted": isBlacklisted,
		}).Info("updating builder status")

		var status datastore.BlockBuilderStatus
		if isBlacklisted {
			status = datastore.RedisBlockBuilderStatusBlacklisted
		} else if isHighPrio {
			status = datastore.RedisBlockBuilderStatusHighPrio
		}

		err := api.redis.SetBlockBuilderStatus(builderPubkey, status)
		if err != nil {
			api.log.WithError(err).Error("could not set block builder status in redis")
		}

		err = api.db.SetBlockBuilderStatus(builderPubkey, isHighPrio, isBlacklisted)
		if err != nil {
			api.log.WithError(err).Error("could not set block builder status in database")
		}

		api.RespondOK(w, struct{ newStatus string }{newStatus: string(status)})
	}
}

// -----------
//  DATA APIS
// -----------

func (api *RelayAPI) handleDataProposerPayloadDelivered(w http.ResponseWriter, req *http.Request) {
	var err error
	args := req.URL.Query()

	filters := database.GetPayloadsFilters{
		Limit: 200,
	}

	if args.Get("slot") != "" && args.Get("cursor") != "" {
		api.RespondError(w, http.StatusBadRequest, "cannot specify both slot and cursor")
		return
	} else if args.Get("slot") != "" {
		filters.Slot, err = strconv.ParseUint(args.Get("slot"), 10, 64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid slot argument")
			return
		}
	} else if args.Get("cursor") != "" {
		filters.Cursor, err = strconv.ParseUint(args.Get("cursor"), 10, 64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid cursor argument")
			return
		}
	}

	if args.Get("block_hash") != "" {
		var hash types.Hash
		err = hash.UnmarshalText([]byte(args.Get("block_hash")))
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid block_hash argument")
			return
		}
		filters.BlockHash = args.Get("block_hash")
	}

	if args.Get("block_number") != "" {
		filters.BlockNumber, err = strconv.ParseUint(args.Get("block_number"), 10, 64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid block_number argument")
			return
		}
	}

	if args.Get("proposer_pubkey") != "" {
		if err = checkBLSPublicKeyHex(args.Get("proposer_pubkey")); err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid proposer_pubkey argument")
			return
		}
		filters.ProposerPubkey = args.Get("proposer_pubkey")
	}

	if args.Get("builder_pubkey") != "" {
		if err = checkBLSPublicKeyHex(args.Get("builder_pubkey")); err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid builder_pubkey argument")
			return
		}
		filters.BuilderPubkey = args.Get("builder_pubkey")
	}

	if args.Get("limit") != "" {
		_limit, err := strconv.ParseUint(args.Get("limit"), 10, 64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid limit argument")
			return
		}
		if _limit > filters.Limit {
			api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("maximum limit is %d", filters.Limit))
			return
		}
		filters.Limit = _limit
	}

	if args.Get("order_by") == "value" {
		filters.OrderByValue = 1
	} else if args.Get("order_by") == "-value" {
		filters.OrderByValue = -1
	}

	deliveredPayloads, err := api.db.GetRecentDeliveredPayloads(filters)
	if err != nil {
		api.log.WithError(err).Error("error getting recent payloads")
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := []BidTraceJSON{}
	for _, payload := range deliveredPayloads {
		trace := BidTraceJSON{
			Slot:                 payload.Slot,
			ParentHash:           payload.ParentHash,
			BlockHash:            payload.BlockHash,
			BuilderPubkey:        payload.BuilderPubkey,
			ProposerPubkey:       payload.ProposerPubkey,
			ProposerFeeRecipient: payload.ProposerFeeRecipient,
			GasLimit:             payload.GasLimit,
			GasUsed:              payload.GasUsed,
			Value:                payload.Value,
		}
		response = append(response, trace)
	}

	api.RespondOK(w, response)
}

func (api *RelayAPI) handleDataBuilderBidsReceived(w http.ResponseWriter, req *http.Request) {
	var err error
	args := req.URL.Query()

	filters := database.GetBuilderSubmissionsFilters{
		Limit:         200,
		Slot:          0,
		BlockHash:     "",
		BlockNumber:   0,
		BuilderPubkey: "",
	}

	if args.Get("cursor") != "" {
		api.RespondError(w, http.StatusBadRequest, "cursor argument not supported on this API")
		return
	}

	if args.Get("slot") != "" && args.Get("cursor") != "" {
		api.RespondError(w, http.StatusBadRequest, "cannot specify both slot and cursor")
		return
	} else if args.Get("slot") != "" {
		filters.Slot, err = strconv.ParseUint(args.Get("slot"), 10, 64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid slot argument")
			return
		}
	}

	if args.Get("block_hash") != "" {
		var hash types.Hash
		err = hash.UnmarshalText([]byte(args.Get("block_hash")))
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid block_hash argument")
			return
		}
		filters.BlockHash = args.Get("block_hash")
	}

	if args.Get("block_number") != "" {
		filters.BlockNumber, err = strconv.ParseUint(args.Get("block_number"), 10, 64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid block_number argument")
			return
		}
	}

	if args.Get("builder_pubkey") != "" {
		if err = checkBLSPublicKeyHex(args.Get("builder_pubkey")); err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid builder_pubkey argument")
			return
		}
		filters.BuilderPubkey = args.Get("builder_pubkey")
	}

	if args.Get("limit") != "" {
		_limit, err := strconv.ParseUint(args.Get("limit"), 10, 64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid limit argument")
			return
		}
		if _limit > filters.Limit {
			api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("maximum limit is %d", filters.Limit))
			return
		}
		filters.Limit = _limit
	}

	deliveredPayloads, err := api.db.GetBuilderSubmissions(filters)
	if err != nil {
		api.log.WithError(err).Error("error getting recent payloads")
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := []BidTraceWithTimestampJSON{}
	for _, payload := range deliveredPayloads {
		trace := BidTraceWithTimestampJSON{
			Timestamp: payload.InsertedAt.Unix(),
			BidTraceJSON: BidTraceJSON{
				Slot:                 payload.Slot,
				ParentHash:           payload.ParentHash,
				BlockHash:            payload.BlockHash,
				BuilderPubkey:        payload.BuilderPubkey,
				ProposerPubkey:       payload.ProposerPubkey,
				ProposerFeeRecipient: payload.ProposerFeeRecipient,
				GasLimit:             payload.GasLimit,
				GasUsed:              payload.GasUsed,
				Value:                payload.Value,
			},
		}
		response = append(response, trace)
	}

	api.RespondOK(w, response)
}

func (api *RelayAPI) handleDataValidatorRegistration(w http.ResponseWriter, req *http.Request) {
	pkStr := req.URL.Query().Get("pubkey")
	if pkStr == "" {
		api.RespondError(w, http.StatusBadRequest, "missing pubkey argument")
		return
	}

	var pk types.PublicKey
	err := pk.UnmarshalText([]byte(pkStr))
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "invalid pubkey")
		return
	}

	registration, err := api.redis.GetValidatorRegistration(types.NewPubkeyHex(pkStr))
	if err != nil {
		api.log.WithError(err).Error("error getting validator registration")
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if registration == nil {
		api.RespondError(w, http.StatusBadRequest, "no registration found for validator "+pkStr)
		return
	}

	api.RespondOK(w, registration)
}
