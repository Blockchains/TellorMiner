package tracker

import (
	"testing"
	"time"
)

func TestFetchRetry(t *testing.T) {
	req := &FetchRequest{queryURL:"https://api.binance.com/api/v1/klines?symbol=ETHBTC&interval=1d&limit=1", timeout: time.Duration(5 * time.Second)}

	res, err := fetchWithRetries(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Result from query: %s\n", string(res))

}
