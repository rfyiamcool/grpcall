package grpcall

import (
	"fmt"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
)

var (
	nullTypes = ReqRespTypes{}
)

type ReqRespTypes struct {
	reqType        proto.Message
	respType       proto.Message
	lastUpdateTime time.Time
}

func (p *ReqRespTypes) isExpired(interval time.Duration) bool {
	if time.Now().Before(p.lastUpdateTime.Add(interval)) {
		return true
	}

	return false
}

type protoTypesCache struct {
	cache sync.Map

	syncInterval time.Duration
}

func newProtoTypeCache() *protoTypesCache {
	p := &protoTypesCache{}
	p.init()
	return p
}

func (p *protoTypesCache) init() {
	p.cache = sync.Map{}
}

func (p *protoTypesCache) get(fmth string) (ReqRespTypes, bool) {
	model, ok := p.cache.Load(fmth)
	if !ok {
		return nullTypes, false
	}

	return model.(ReqRespTypes), ok
}

func (p *protoTypesCache) getRequestType(fmth string) (proto.Message, bool) {
	model, ok := p.cache.Load(fmth)
	if !ok {
		return nil, false
	}

	return model.(proto.Message), ok
}

func (p *protoTypesCache) getRespType(fmth string) (proto.Message, bool) {
	model, ok := p.cache.Load(fmth)
	if !ok {
		return nil, false
	}

	return model.(proto.Message), ok
}

func (p *protoTypesCache) set(fmth string, reqType, respType proto.Message) error {
	p.cache.Store(fmth, ReqRespTypes{
		reqType:        reqType,
		respType:       respType,
		lastUpdateTime: time.Now(),
	})

	return nil
}

func (p *protoTypesCache) reset() {
	p.cache = sync.Map{}
}

func (p *protoTypesCache) makeKey(svc, mth string) string {
	return fmt.Sprintf("%s/%s", svc, mth)
}

func (p *protoTypesCache) clean() {
	p.cache = sync.Map{}
}
