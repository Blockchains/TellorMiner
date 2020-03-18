package ops

import (
	"context"
	"fmt"
	"os"
	"time"

	tellorCommon "github.com/tellor-io/TellorMiner/common"
	"github.com/tellor-io/TellorMiner/config"
	"github.com/tellor-io/TellorMiner/db"
	"github.com/tellor-io/TellorMiner/pow"
	"github.com/tellor-io/TellorMiner/util"
)

type WorkSource interface {
	GetWork() *pow.Work
}

type SolutionSink interface {
	Submit(context.Context, *pow.Result)
}

//MiningMgr holds items for mining and requesting data
type MiningMgr struct {
	//primary exit channel
	exitCh  chan os.Signal
	log     *util.Logger
	Running bool

	group      *pow.MiningGroup
	tasker     WorkSource
	solHandler SolutionSink

	dataRequester *DataRequester
	//data requester's exit channel
	requesterExit chan os.Signal
}

//CreateMiningManager creates a new manager that mananges mining and data requests
func CreateMiningManager(ctx context.Context, exitCh chan os.Signal, submitter tellorCommon.TransactionSubmitter) (*MiningMgr, error) {
	cfg := config.GetConfig()

	group, err := pow.SetupMiningGroup(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to setup miners: %s", err.Error())
	}

	mng := &MiningMgr{
		exitCh:     exitCh,
		log:        util.NewLogger("ops", "MiningMgr"),
		Running:    false,
		group:      group,
		tasker:     nil,
		solHandler: nil,
	}

	if cfg.EnablePoolWorker {
		pool := pow.CreatePool(cfg, group)
		mng.tasker = pool
		mng.solHandler = pool
	} else {
		proxy := ctx.Value(tellorCommon.DataProxyKey).(db.DataServerProxy)
		mng.tasker = pow.CreateTasker(cfg, proxy)
		mng.solHandler = pow.CreateSolutionHandler(cfg, submitter, proxy)
		if cfg.RequestData > 0 {
			fmt.Println("dataRequester created")
			fmt.Println("Request Interval: ", cfg.RequestDataInterval.Duration)
			mng.dataRequester = CreateDataRequester(exitCh, submitter, cfg.RequestDataInterval.Duration, proxy)
		}
	}
	return mng, nil
}

//Start will start the mining run loop
func (mgr *MiningMgr) Start(ctx context.Context) {
	mgr.Running = true
	go func(ctx context.Context) {
		cfg := config.GetConfig()

		ticker := time.NewTicker(cfg.MiningInterruptCheckInterval.Duration)

		//if you make these buffered, think about the effects on synchronization!
		input := make(chan *pow.Work)
		output := make(chan *pow.Result)
		if cfg.RequestData > 0 {
			fmt.Println("Starting Data Requester")
			mgr.dataRequester.Start(ctx)
		}
		//start the mining group
		go mgr.group.Mine(input, output)

		// sends work to the mining group
		sendWork := func() {
			//if its nil, nothing new to report
			work := mgr.tasker.GetWork()
			if work != nil {
				input <- work
			}
		}
		//send the initial challenge
		sendWork()
		for {
			select {
			//boss wants us to quit for the day
			case <-mgr.exitCh:
				//exit
				input <- nil

			//found a solution
			case result := <-output:
				if result == nil {
					mgr.Running = false
					return
				}
				mgr.solHandler.Submit(ctx, result)
				sendWork()

			//time to check for a new challenge
			case _ = <-ticker.C:
				sendWork()
			}
		}
	}(ctx)
}
