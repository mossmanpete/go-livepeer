package discovery

import (
	"context"
	"math/rand"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/livepeer/go-livepeer/core"
	"github.com/livepeer/go-livepeer/net"
	"github.com/livepeer/go-livepeer/server"

	"github.com/golang/glog"
)

const GetOrchestratorsTimeoutLoop = 1 * time.Hour

type rpcMethods interface {
	getOrchestrator(context.Context, *url.URL) (*net.OrchestratorInfo, error)
}

type orchestratorPool struct {
	uris  []*url.URL
	bcast server.Broadcaster
	rpc   rpcMethods
}

func (o *orchestratorPool) getOrchestrator(ctx context.Context, orchestratorServer *url.URL) (*net.OrchestratorInfo, error) {
	return server.GetOrchestratorInfo(ctx, o.bcast, orchestratorServer)
}

func NewOrchestratorPool(node *core.LivepeerNode, addresses []string) *orchestratorPool {
	var uris []*url.URL

	for _, addr := range addresses {
		if !strings.HasPrefix(addr, "http") {
			addr = "https://" + addr
		}
		uri, err := url.ParseRequestURI(addr)
		if err != nil {
			glog.Error("Could not parse orchestrator URI: ", err)
			continue
		}
		uris = append(uris, uri)
	}

	if len(uris) <= 0 {
		glog.Error("Could not parse orchAddresses given - no URIs returned ")
	}

	var randomizedUris []*url.URL
	r := rand.New(rand.NewSource(time.Now().Unix()))
	for _, i := range r.Perm(len(uris)) {
		uri := uris[i]
		randomizedUris = append(randomizedUris, uri)
	}
	bcast := core.NewBroadcaster(node)
	return &orchestratorPool{bcast: bcast, uris: randomizedUris}
}

func NewOnchainOrchestratorPool(node *core.LivepeerNode) *orchestratorPool {
	// if livepeer running in offchain mode, return nil
	if node.Eth == nil {
		glog.Error("Could not refresh DB list of orchestrators: LivepeerNode nil")
		return nil
	}

	orchestrators, err := node.Eth.RegisteredTranscoders()
	if err != nil {
		glog.Error("Could not refresh DB list of orchestrators: ", err)
		return nil
	}

	var addresses []string
	for _, orch := range orchestrators {
		addresses = append(addresses, orch.ServiceURI)
	}

	return NewOrchestratorPool(node, addresses)
}

func (o *orchestratorPool) GetOrchestrators(numOrchestrators int) ([]*net.OrchestratorInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GetOrchestratorsTimeoutLoop)
	orchInfos := []*net.OrchestratorInfo{}
	orchChan := make(chan struct{})
	numResp := 0
	numSuccessResp := 0
	respLock := sync.Mutex{}
	orchChanLock := sync.Mutex{}

	getOrchInfo := func(uri *url.URL) {
		info, err := o.rpc.getOrchestrator(ctx, uri)
		respLock.Lock()
		defer respLock.Unlock()
		numResp++
		if err == nil {
			orchInfos = append(orchInfos, info)
			numSuccessResp++
		}
		if numSuccessResp >= numOrchestrators || numResp >= len(o.uris) {
			orchChan <- struct{}{}
		}
	}

	for _, uri := range o.uris {
		go getOrchInfo(uri)
	}

	select {
	case <-ctx.Done():
		glog.Error("Done fetching orch info for orchestrators, context timeout: ", orchInfos)
		cancel()
		return orchInfos[:numOrchestrators], nil
	case <-orchChan:
		orchChanLock.Lock()
		returnOrchs := orchInfos[:numOrchestrators]
		orchChanLock.Unlock()
		glog.Error("Done fetching orch info for orchestrators, numResponses fetched: ", orchInfos)
		cancel()
		return returnOrchs, nil
	}
}
