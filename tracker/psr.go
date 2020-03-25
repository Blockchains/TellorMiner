package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	tellorCommon "github.com/tellor-io/TellorMiner/common"
	"github.com/tellor-io/TellorMiner/config"
	"github.com/tellor-io/TellorMiner/db"
	"github.com/tellor-io/TellorMiner/util"
)

//PSRTracker keeps track of pre-specified requests
type PSRTracker struct {
	Requests    []PrespecifiedRequest `json:"prespecifiedRequests"`
}

//PrespecifiedRequest holds fields for pre-specific requests
type PrespecifiedRequest struct {
	RequestID      uint     `json:"requestID"`
	APIs           []string `json:"apis"`
	Transformation string   `json:"transformation"`
	Granularity    uint     `json:"granularity"`
	Symbol         string   `json:"symbol"`
}

var (
	psrLog = util.NewLogger("tracker", "PSRTracker")
	funcs  map[string]interface{}
)

//BuildPSRTrackers creates and initializes a new tracker instance
func BuildPSRTrackers() ([]Tracker, error) {

	psr := &PSRTracker{}
	if err := psr.init(); err != nil {
		return nil, err
	}
	funcs = map[string]interface{}{
		"value":   value,
		"average": average,
		"median":  median,
		"square":  square,
	}
	trackers := make([]Tracker, len(psr.Requests))
	for i := range trackers {
		trackers[i] = psr.Requests[i]
	}
	return trackers, nil
}

func GetPSRByIDMap() (map[uint]*PrespecifiedRequest, error) {
	result := make(map[uint]*PrespecifiedRequest)
	psr := &PSRTracker{}
	if err := psr.init(); err != nil {
		return nil, err
	}
	for i := 0; i < len(psr.Requests); i++ {
		r := psr.Requests[i]
		result[r.RequestID] = &r
	}
	return result, nil
}

func (psr *PSRTracker) init() error {
	//Loop through all PSRs
	cfg := config.GetConfig()

	psrPath := filepath.Join(cfg.PSRFolder, "psr.json")
	byteValue, err := ioutil.ReadFile(psrPath)
	if err != nil {
		return fmt.Errorf("failed to read psr file @ %s: %v", psrPath, err)
	}

	// we unmarshal our byteArray which contains our
	// jsonFile's content into 'Requests' which we defined above
	err = json.Unmarshal(byteValue, &psr)
	if err != nil {
		return err
	}

	err = EnsureValueOracle()
	if err != nil {
		return fmt.Errorf("failed to launch value oracle: %v", err)
	}

	psrLog.Info("Initialized PSR with %d requests\n", len(psr.Requests))
	return nil
}

//Exec implements tracker API
func (r PrespecifiedRequest) Exec(ctx context.Context) error {
	//TODO: retrieve github updates of psr config file. For now, we'll just pull
	//PSR's as defined by psr.json file
	result := r.fetch()
	DB := ctx.Value(tellorCommon.DBContextKey).(db.DB)
	if result.err != nil {
		psrLog.Error("Problem fetching PSR for id %d: %v]\n", result.r.RequestID, result.err)
	} else {
		setRequestValue(DB, uint64(result.r.RequestID), result.val.Created, big.NewInt(int64(result.val.Val)))
	}
	return nil
}

func (r PrespecifiedRequest) String() string {
	return fmt.Sprintf("%s PSR", r.Symbol)
}

type fetchResult struct {
	r   *PrespecifiedRequest
	val *TimedInt
	err error
}

func (r PrespecifiedRequest) fetch() *fetchResult {
	cfg := config.GetConfig()
	reqs := make([]*FetchRequest, len(r.APIs))
	argGroups := make([][]string, len(r.APIs))
	for i := 0; i < len(r.APIs); i++ {
		api := r.APIs[i]
		url, args := util.ParseQueryString(api)
		reqs[i] = &FetchRequest{queryURL: url, timeout: cfg.FetchTimeout.Duration}
		argGroups[i] = args
	}
	payloads, _ := batchFetchWithRetries(reqs)
	vals := make([]int, 0, len(payloads))
	errs := 0
	for i, pl := range payloads {
		if pl == nil {
			errs += 1
			continue
		}
		v, err := util.ParsePayload(pl, r.Granularity, argGroups[i])
		if err != nil {
			errs += 1
			continue
		}
		vals = append(vals, v)
	}
	result := &fetchResult{r: &r}
	if len(vals) > 0 {
		res, err := computeTransformation(r.Transformation, vals)
		if err != nil {
			result.err = err
		} else {
			result.val = &TimedInt{
				Created: time.Now(),
				Val:     res.Interface().(uint),
			}
		}
	} else {
		result.err = fmt.Errorf("no sucessful api hits, no value stored for id %d", r.RequestID)
	}
	return result
}

func computeTransformation(name string, params ...interface{}) (result reflect.Value, err error) {
	f := reflect.ValueOf(funcs[name])
	if len(params) != f.Type().NumIn() {
		err = errors.New("The number of params is not adapted")
		return
	}
	in := make([]reflect.Value, len(params))
	for k, param := range params {
		in[k] = reflect.ValueOf(param)
	}
	result = f.Call(in)[0]
	return result, nil
}

func value(num []int) uint {
	//fmt.Println("Calling Value", num)
	return uint(num[0])
}

func average(nums []int) uint {
	sum := 0
	for i := 0; i < len(nums); i++ {
		sum += nums[i]
	}
	//fmt.Println("Average", sum/len(nums))
	return uint(sum / len(nums))
}

func median(num []int) uint {
	sort.Ints(num)
	//fmt.Println("Median", num[len(num)/2])
	return uint(num[len(num)/2])
}

func square(num int) int {
	//fmt.Println("Square", num*num)
	return num * num
}
