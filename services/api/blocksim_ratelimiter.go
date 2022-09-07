package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/go-utils/cli"
	"github.com/flashbots/go-utils/jsonrpc"
)

var (
	ErrRequestClosed    = errors.New("request context closed")
	ErrSimulationFailed = errors.New("simulation failed")
)

var maxConcurrentBlocks = int64(cli.GetEnvInt("BLOCKSIM_MAX_CONCURRENT", 4))

type BlockSimulationRateLimiter struct {
	cv          *sync.Cond
	counter     int64
	blockSimURL string
}

func NewBlockSimulationRateLimiter(blockSimURL string) *BlockSimulationRateLimiter {
	return &BlockSimulationRateLimiter{
		cv:          sync.NewCond(&sync.Mutex{}),
		counter:     0,
		blockSimURL: blockSimURL,
	}
}

func (b *BlockSimulationRateLimiter) send(context context.Context, payload *types.BuilderSubmitBlockRequest) error {
	b.cv.L.Lock()
	cnt := atomic.AddInt64(&b.counter, 1)
	if cnt > maxConcurrentBlocks {
		b.cv.Wait()
	}
	b.cv.L.Unlock()

	defer func() {
		b.cv.L.Lock()
		atomic.AddInt64(&b.counter, -1)
		b.cv.Signal()
		b.cv.L.Unlock()
	}()

	if err := context.Err(); err != nil {
		return ErrRequestClosed
	}

	simReq := jsonrpc.NewJSONRPCRequest("1", "flashbots_validateBuilderSubmissionV1", payload)
	simResp, err := jsonrpc.SendJSONRPCRequest(*simReq, b.blockSimURL)
	if err != nil {
		return err
	} else if simResp.Error != nil {
		return fmt.Errorf("%w: %s", ErrSimulationFailed, simResp.Error.Message)
	}

	return nil
}

// currentCounter returns the number of waiting and active requests
func (b *BlockSimulationRateLimiter) currentCounter() int64 {
	return atomic.LoadInt64(&b.counter)
}
