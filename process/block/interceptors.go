package block

import (
	"github.com/ElrondNetwork/elrond-go-sandbox/data"
	"github.com/ElrondNetwork/elrond-go-sandbox/hashing"
	"github.com/ElrondNetwork/elrond-go-sandbox/p2p"
	"github.com/ElrondNetwork/elrond-go-sandbox/process"
	"github.com/ElrondNetwork/elrond-go-sandbox/sharding"
	"github.com/ElrondNetwork/elrond-go-sandbox/storage"
)

// HeaderInterceptor represents an interceptor used for block headers
type HeaderInterceptor struct {
	process.Interceptor
	headers          data.ShardedDataCacherNotifier
	headersNonces    data.Uint64Cacher
	hasher           hashing.Hasher
	shardCoordinator sharding.ShardCoordinator
}

// GenericBlockBodyInterceptor represents an interceptor used for all types of block bodies
type GenericBlockBodyInterceptor struct {
	process.Interceptor
	cache            storage.Cacher
	hasher           hashing.Hasher
	shardCoordinator sharding.ShardCoordinator
}

//------- HeaderInterceptor

// NewHeaderInterceptor hooks a new interceptor for block headers
// Fetched block headers will be placed in a data pool
func NewHeaderInterceptor(
	interceptor process.Interceptor,
	headers data.ShardedDataCacherNotifier,
	headersNonces data.Uint64Cacher,
	hasher hashing.Hasher,
	shardCoordinator sharding.ShardCoordinator,
) (*HeaderInterceptor, error) {

	if interceptor == nil {
		return nil, process.ErrNilInterceptor
	}

	if headers == nil {
		return nil, process.ErrNilHeadersDataPool
	}

	if headersNonces == nil {
		return nil, process.ErrNilHeadersNoncesDataPool
	}

	if hasher == nil {
		return nil, process.ErrNilHasher
	}

	if shardCoordinator == nil {
		return nil, process.ErrNilShardCoordinator
	}

	hdrIntercept := &HeaderInterceptor{
		Interceptor:      interceptor,
		headers:          headers,
		headersNonces:    headersNonces,
		hasher:           hasher,
		shardCoordinator: shardCoordinator,
	}

	interceptor.SetCheckReceivedObjectHandler(hdrIntercept.processHdr)

	return hdrIntercept, nil
}

func (hi *HeaderInterceptor) processHdr(hdr p2p.Creator, rawData []byte) error {
	if hdr == nil {
		return process.ErrNilBlockHeader
	}

	if rawData == nil {
		return process.ErrNilDataToProcess
	}

	hdrIntercepted, ok := hdr.(process.HeaderInterceptorAdapter)

	if !ok {
		return process.ErrBadInterceptorTopicImplementation
	}

	hash := hi.hasher.Compute(string(rawData))
	hdrIntercepted.SetHash(hash)

	err := hdrIntercepted.IntegrityAndValidity(hi.shardCoordinator)
	if err != nil {
		return err
	}

	err = hdrIntercepted.VerifySig()
	if err != nil {
		return err
	}

	hi.headers.AddData(hash, hdrIntercepted, hdrIntercepted.Shard())
	if hi.checkHeaderForCurrentShard(hdrIntercepted) {
		_, _ = hi.headersNonces.HasOrAdd(hdrIntercepted.GetHeader().Nonce, hash)
	}
	return nil
}

func (hi *HeaderInterceptor) checkHeaderForCurrentShard(header process.HeaderInterceptorAdapter) bool {
	//TODO add real logic here
	return true
}

//------- GenericBlockBodyInterceptor

// NewGenericBlockBodyInterceptor hooks a new interceptor for block bodies
// Fetched data blocks will be placed inside the cache
func NewGenericBlockBodyInterceptor(
	interceptor process.Interceptor,
	cache storage.Cacher,
	hasher hashing.Hasher,
	templateObj process.BlockBodyInterceptorAdapter,
	shardCoordinator sharding.ShardCoordinator,
) (*GenericBlockBodyInterceptor, error) {

	if interceptor == nil {
		return nil, process.ErrNilInterceptor
	}

	if cache == nil {
		return nil, process.ErrNilCacher
	}

	if hasher == nil {
		return nil, process.ErrNilHasher
	}

	if templateObj == nil {
		return nil, process.ErrNilTemplateObj
	}

	if shardCoordinator == nil {
		return nil, process.ErrNilShardCoordinator
	}

	bbIntercept := &GenericBlockBodyInterceptor{
		Interceptor:      interceptor,
		cache:            cache,
		hasher:           hasher,
		shardCoordinator: shardCoordinator,
	}

	interceptor.SetCheckReceivedObjectHandler(bbIntercept.processBodyBlock)

	return bbIntercept, nil
}

func (gbbi *GenericBlockBodyInterceptor) processBodyBlock(bodyBlock p2p.Creator, rawData []byte) error {
	if bodyBlock == nil {
		return process.ErrNilBlockBody
	}

	if rawData == nil {
		return process.ErrNilDataToProcess
	}

	blockBodyIntercepted, ok := bodyBlock.(process.BlockBodyInterceptorAdapter)

	if !ok {
		return process.ErrBadInterceptorTopicImplementation
	}

	hash := gbbi.hasher.Compute(string(rawData))
	blockBodyIntercepted.SetHash(hash)

	err := blockBodyIntercepted.IntegrityAndValidity(gbbi.shardCoordinator)
	if err != nil {
		return err
	}

	_ = gbbi.cache.Put(hash, blockBodyIntercepted)
	return nil
}