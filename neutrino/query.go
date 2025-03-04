// NOTE: THIS API IS UNSTABLE RIGHT NOW.

package neutrino

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkt-cash/pktd/btcutil/er"
	"github.com/pkt-cash/pktd/pktlog/log"

	"github.com/davecgh/go-spew/spew"
	"github.com/pkt-cash/pktd/blockchain"
	"github.com/pkt-cash/pktd/btcutil"
	"github.com/pkt-cash/pktd/btcutil/gcs"
	"github.com/pkt-cash/pktd/btcutil/gcs/builder"
	"github.com/pkt-cash/pktd/chaincfg/chainhash"
	"github.com/pkt-cash/pktd/neutrino/cache"
	"github.com/pkt-cash/pktd/neutrino/pushtx"
	"github.com/pkt-cash/pktd/wire"
)

var (
	// QueryTimeout specifies how long to wait for a peer to answer a
	// query.
	QueryTimeout = time.Second * 10

	// QueryBatchTimout is the total time we'll wait for a batch fetch
	// query to complete.
	// TODO(halseth): instead use timeout since last received response?
	QueryBatchTimeout = time.Second * 30

	// QueryPeerCooldown is the time we'll wait before re-assigning a query
	// to a peer that previously failed because of a timeout.
	QueryPeerCooldown = time.Second * 5

	// QueryNumRetries specifies how many times to retry sending a query to
	// each peer before we've concluded we aren't going to get a valid
	// response. This allows to make up for missed messages in some
	// instances.
	QueryNumRetries = 2

	// QueryPeerConnectTimeout specifies how long to wait for the
	// underlying chain service to connect to a peer before giving up
	// on a query in case we don't have any peers.
	QueryPeerConnectTimeout = time.Second * 30

	// QueryEncoding specifies the default encoding (witness or not) for
	// `getdata` and other similar messages.
	QueryEncoding = wire.WitnessEncoding
)

// QueryAccess is an interface that gives acces to query a set of peers in
// different ways.
type QueryAccess interface {
	queryAllPeers(
		queryMsg wire.Message,
		checkResponse func(sp *ServerPeer, resp wire.Message,
			quit chan<- struct{}, peerQuit chan<- struct{}),
		options ...QueryOption)
}

// A compile-time check to ensure that ChainService implements the
// QueryAccess interface.
var _ QueryAccess = (*ChainService)(nil)

// queries are a set of options that can be modified per-query, unlike global
// options.
//
// TODO: Make more query options that override global options.
type queryOptions struct {
	// timeout lets the query know how long to wait for a peer to answer
	// the query before moving onto the next peer.
	timeout time.Duration

	// numRetries tells the query how many times to retry asking each peer
	// the query.
	numRetries uint8

	// peerConnectTimeout lets the query know how long to wait for the
	// underlying chain service to connect to a peer before giving up
	// on a query in case we don't have any peers.
	peerConnectTimeout time.Duration

	// encoding lets the query know which encoding to use when queueing
	// messages to a peer.
	encoding wire.MessageEncoding

	// doneChan lets the query signal the caller when it's done, in case
	// it's run in a goroutine.
	doneChan chan<- struct{}

	// optimisticBatch indicates whether we expect more calls to follow,
	// and that we should attempt to batch more items with the query such
	// that they can be cached, avoiding the extra round trip.
	optimisticBatch optimisticBatchType
}

// optimisticBatchType is a type indicating the kind of batching we want to
// execute with a query.
type optimisticBatchType uint8

const (
	// noBatch indicates no other than the specified item should be
	// queried.
	noBatch optimisticBatchType = iota

	// forwardBatch is used to indicate we should also query for items
	// following, as they most likely will be fetched next.
	forwardBatch

	// reverseBatch is used to indicate we should also query for items
	// preceding, as they most likely will be fetched next.
	reverseBatch
)

// QueryOption is a functional option argument to any of the network query
// methods, such as GetBlock and GetCFilter (when that resorts to a network
// query). These are always processed in order, with later options overriding
// earlier ones.
type QueryOption func(*queryOptions)

// defaultQueryOptions returns a queryOptions set to package-level defaults.
func defaultQueryOptions() *queryOptions {
	return &queryOptions{
		timeout:            QueryTimeout,
		numRetries:         uint8(QueryNumRetries),
		peerConnectTimeout: QueryPeerConnectTimeout,
		encoding:           QueryEncoding,
		optimisticBatch:    noBatch,
	}
}

// applyQueryOptions updates a queryOptions set with functional options.
func (qo *queryOptions) applyQueryOptions(options ...QueryOption) {
	for _, option := range options {
		option(qo)
	}
}

// Timeout is a query option that lets the query know how long to wait for each
// peer we ask the query to answer it before moving on.
func Timeout(timeout time.Duration) QueryOption {
	return func(qo *queryOptions) {
		qo.timeout = timeout
	}
}

// NumRetries is a query option that lets the query know the maximum number of
// times each peer should be queried. The default is one.
func NumRetries(numRetries uint8) QueryOption {
	return func(qo *queryOptions) {
		qo.numRetries = numRetries
	}
}

// PeerConnectTimeout is a query option that lets the query know how long to
// wait for the underlying chain service to connect to a peer before giving up
// on a query in case we don't have any peers.
func PeerConnectTimeout(timeout time.Duration) QueryOption {
	return func(qo *queryOptions) {
		qo.peerConnectTimeout = timeout
	}
}

// Encoding is a query option that allows the caller to set a message encoding
// for the query messages.
func Encoding(encoding wire.MessageEncoding) QueryOption {
	return func(qo *queryOptions) {
		qo.encoding = encoding
	}
}

// DoneChan allows the caller to pass a channel that will get closed when the
// query is finished.
func DoneChan(doneChan chan<- struct{}) QueryOption {
	return func(qo *queryOptions) {
		qo.doneChan = doneChan
	}
}

// OptimisticBatch allows the caller to tell that items following the requested
// one should be included in the query.
func OptimisticBatch() QueryOption {
	return func(qo *queryOptions) {
		qo.optimisticBatch = forwardBatch
	}
}

// OptimisticReverseBatch allows the caller to tell that items preceding the
// requested one should be included in the query.
func OptimisticReverseBatch() QueryOption {
	return func(qo *queryOptions) {
		qo.optimisticBatch = reverseBatch
	}
}

// queryState is an atomically updated per-query state for each query in a
// batch.
//
// State transitions are:
//
// * queryWaitSubmit->queryWaitResponse - send query to peer
// * queryWaitResponse->queryWaitSubmit - query timeout with no acceptable
//   response
// * queryWaitResponse->queryAnswered - acceptable response to query received
type queryState uint32

const (
	// Waiting to be submitted to a peer.
	queryWaitSubmit queryState = iota

	// Submitted to a peer, waiting for reply.
	queryWaitResponse

	// Valid reply received.
	queryAnswered
)

// We provide 3 kinds of queries:
//
// * queryAllPeers allows a single query to be broadcast to all peers, and
//   then waits for as many peers as possible to answer that query within
//   a timeout. This allows for doing things like checking cfilter checkpoints.
//
// * queryPeers allows a single query to be passed to one peer at a time until
//   the query is deemed answered. This is good for getting a single piece of
//   data, such as a filter or a block.
//
// * queryBatch allows a batch of queries to be distributed among all peers,
//   recirculating upon timeout.
//
// TODO(aakselrod): maybe abstract the query scheduler into a functional option
// and provide some presets (including the ones below) prior to factoring out
// the query API into its own package?

// queryChainServiceBatch is a helper function that sends a batch of queries to
// the entire pool of peers of the given ChainService, attempting to get them
// all answered unless the quit channel is closed. It continues to update its
// view of the connected peers in case peers connect or disconnect during the
// query. The package-level QueryTimeout parameter, overridable by the Timeout
// option, determines how long a peer waits for a query before moving onto the
// next one. The NumRetries option and the QueryNumRetries package-level
// variable are ignored; the query continues until it either completes or the
// passed quit channel is closed.  For memory efficiency, we attempt to get
// responses as close to ordered as we can, so that the caller can cache as few
// responses as possible before committing to storage.
//
// TODO(aakselrod): support for more than one in-flight query per peer to
// reduce effects of latency.
func queryChainServiceBatch(
	// s is the ChainService to use.
	s *ChainService,

	// queryMsgs is a slice of queries for which the caller wants responses.
	queryMsgs []wire.Message,

	// checkResponse is called for every received message to see if it
	// answers the query message. It should return true if so.
	checkResponse func(sp *ServerPeer, query wire.Message,
		resp wire.Message) bool,

	// queryQuit forces the query to end before it's complete.
	queryQuit <-chan struct{},

	// options takes functional options for executing the query.
	options ...QueryOption) {

	// Starting with the set of default options, we'll apply any specified
	// functional options to the query.
	qo := defaultQueryOptions()
	qo.applyQueryOptions(options...)

	// Shared state between this goroutine and the per-peer goroutines.
	queryStates := make([]uint32, len(queryMsgs))

	// subscription allows us to subscribe to notifications from peers.
	msgChan := make(chan spMsg, len(queryMsgs))
	subQuit := make(chan struct{})
	subscription := spMsgSubscription{
		msgChan:  msgChan,
		quitChan: subQuit,
	}
	defer close(subQuit)

	// peerState holds a query message and an answer channel that get a
	// notice when the query got a match. If it's the peer's match, the
	// peer can mark the query a success and move on to the next query
	// ahead of timeout.
	type peerState struct {
		msg         wire.Message
		matchSignal chan struct{}
	}

	// peerStates and its companion mutex allow the peer goroutines to
	// tell the main goroutine what query they're currently working on, and
	// for the main goroutine to signal the peer when a match has been
	// received.
	peerStates := make(map[string]peerState)
	var mtxPeerStates sync.RWMutex

	// allDone is a helper closure we'll use to determine whether we can
	// exit due to all of our queries being answered.
	allDone := func() bool {
		for i := 0; i < len(queryStates); i++ {
			if atomic.LoadUint32(&queryStates[i]) !=
				uint32(queryAnswered) {
				return false
			}
		}
		return true
	}

	// allDoneSignal is a channel we'll close to signal to the main loop
	// that all of the queries have been answered.
	var allDoneOnce sync.Once
	allDoneSignal := make(chan struct{})

	peerGoroutine := func(sp *ServerPeer, quit <-chan struct{},
		allDoneSignal chan struct{}) {

		// Subscribe to messages from the peer.
		sp.subscribeRecvMsg(subscription)
		defer sp.unsubscribeRecvMsgs(subscription)
		defer func() {
			mtxPeerStates.Lock()
			delete(peerStates, sp.Addr())
			mtxPeerStates.Unlock()
		}()

		// Track the last query our peer failed to answer and skip over
		// it for the next attempt. This helps prevent most instances
		// of the same peer being asked for the same query every time.
		firstUnfinished, handleQuery := 0, -1

		for firstUnfinished < len(queryMsgs) {
			select {
			case <-queryQuit:
				return
			case <-s.quit:
				return
			case <-quit:
				return
			default:
			}

			handleQuery = -1

			for i := firstUnfinished; i < len(queryMsgs); i++ {
				// If this query is finished and we're at
				// firstUnfinished, update firstUnfinished.
				if i == firstUnfinished &&
					atomic.LoadUint32(&queryStates[i]) ==
						uint32(queryAnswered) {
					firstUnfinished++

					log.Tracef("Query #%v already answered, "+
						"skipping", i)
					continue
				}

				// We check to see if the query is waiting to
				// be handled. If so, we mark it as being
				// handled. If not, we move to the next one.
				if !atomic.CompareAndSwapUint32(
					&queryStates[i],
					uint32(queryWaitSubmit),
					uint32(queryWaitResponse),
				) {
					log.Tracef("Query #%v already being "+
						"queried for, skipping", i)
					continue
				}

				// The query is now marked as in-process. We
				// begin to process it.
				handleQuery = i
				sp.QueueMessageWithEncoding(queryMsgs[i],
					nil, qo.encoding)
				break
			}

			// Regardless of whether we have a query or not, we
			// need a timeout.
			timeout := time.After(qo.timeout)
			if handleQuery == -1 {
				if firstUnfinished == len(queryMsgs) {
					// We've now answered all the queries.
					return
				}

				// We have nothing to work on but not all
				// queries are answered yet. Wait for a query
				// timeout, or a quit signal, then see if
				// anything needs our help.
				select {
				case <-queryQuit:
					return
				case <-s.quit:
					return
				case <-quit:
					return
				case <-timeout:
					if sp.Connected() {
						continue
					} else {
						return
					}
				}
			}

			// We have a query we're working on.
			matchSignal := make(chan struct{}, 1)
			mtxPeerStates.Lock()
			peerStates[sp.Addr()] = peerState{
				msg:         queryMsgs[handleQuery],
				matchSignal: matchSignal,
			}
			mtxPeerStates.Unlock()

			exiting := false
			select {
			case <-queryQuit:
				exiting = true
			case <-s.quit:
				exiting = true
			case <-quit:
				exiting = true

			case <-timeout:
				// We failed, so set the query state back to
				// zero and update our lastFailed state.
				atomic.StoreUint32(
					&queryStates[handleQuery], uint32(queryWaitSubmit),
				)

				// Delete the query from our peer states, to
				// indicate we are no longer expecting a
				// response.
				mtxPeerStates.Lock()
				delete(peerStates, sp.Addr())
				mtxPeerStates.Unlock()

				log.Tracef("Query for #%v failed, moving "+
					"on: %v", handleQuery,
					log.C(func() string {
						return spew.Sdump(queryMsgs[handleQuery])
					}))

				// To allow other peers to pick up this query,
				// let the peer that just timed out wait a
				// cooldown period before handing it the next
				// query.
				select {
				case <-time.After(QueryPeerCooldown):
				case <-queryQuit:
					return
				case <-s.quit:
					return
				case <-quit:
					return
				}

			case _, ok := <-matchSignal:
				if !ok {
					exiting = true
					break
				}

				log.Tracef("Query #%v answered, updating state",
					handleQuery)

				// We got a match signal so we can mark this
				// query a success.
				atomic.StoreUint32(&queryStates[handleQuery],
					uint32(queryAnswered))

				// If we're done answering all of our queries,
				// we can exit now.
				if allDone() {
					allDoneOnce.Do(func() {
						close(allDoneSignal)
					})
					return
				}
			}

			// Before exiting the peer goroutine, reset the query
			// state to ensure other peers can pick it up.
			if exiting {
				atomic.StoreUint32(
					&queryStates[handleQuery], uint32(queryWaitSubmit),
				)

				// Delete the current state of the peer, so we
				// won't process any lingering responses from
				// it.
				mtxPeerStates.Lock()
				delete(peerStates, sp.Addr())
				mtxPeerStates.Unlock()
				return
			}
		}
	}

	// peerQuits holds per-peer quit channels so we can kill the goroutines
	// when they disconnect.
	peerQuits := make(map[string]chan struct{})

	// Clean up on exit.
	defer func() {
		for _, quitChan := range peerQuits {
			close(quitChan)
		}
	}()

	for {
		// Update our view of peers, starting new workers for new peers
		// and removing disconnected/banned peers.
		for _, peer := range s.Peers() {
			sp := peer.Addr()
			if _, ok := peerQuits[sp]; !ok && peer.Connected() {
				peerQuits[sp] = make(chan struct{})

				go peerGoroutine(
					peer, peerQuits[sp], allDoneSignal,
				)
			}

		}

		for peer, quitChan := range peerQuits {
			p := s.PeerByAddr(peer)
			if p == nil || !p.Connected() {
				close(quitChan)
				delete(peerQuits, peer)
			}
		}

		select {
		case msg := <-msgChan:
			mtxPeerStates.RLock()
			curQuery, ok := peerStates[msg.sp.Addr()]
			mtxPeerStates.RUnlock()

			// Break if we didn't expect a response from this peer.
			if !ok {
				break
			}

			if checkResponse(msg.sp, curQuery.msg, msg.msg) {
				select {
				case <-queryQuit:
					return
				case <-s.quit:
					return
				case curQuery.matchSignal <- struct{}{}:
				}
			}
		case <-time.After(qo.timeout):
		case <-allDoneSignal:
			return
		case <-queryQuit:
			return
		case <-s.quit:
			return
		}
	}
}

// queryAllPeers is a helper function that sends a query to all peers and waits
// for a timeout specified by the QueryTimeout package-level variable or the
// Timeout functional option. The NumRetries option is set to 1 by default
// unless overridden by the caller.
func (s *ChainService) queryAllPeers(
	// queryMsg is the message to broadcast to all peers.
	queryMsg wire.Message,

	// checkResponse is called for every message within the timeout period.
	// The quit channel lets the query know to terminate because the
	// required response has been found. This is done by closing the
	// channel. The peerQuit lets the query know to terminate the query for
	// the peer which sent the response, allowing releasing resources for
	// peers which respond quickly while continuing to wait for slower
	// peers to respond and nonresponsive peers to time out.
	checkResponse func(sp *ServerPeer, resp wire.Message,
		quit chan<- struct{}, peerQuit chan<- struct{}),

	// options takes functional options for executing the query.
	options ...QueryOption) {

	// Starting with the set of default options, we'll apply any specified
	// functional options to the query.
	qo := defaultQueryOptions()
	qo.numRetries = 1
	qo.applyQueryOptions(options...)

	// This is done in a single-threaded query because the peerState is
	// held in a single thread. This is the only part of the query
	// framework that requires access to peerState, so it's done once per
	// query.
	peers := s.Peers()

	// This will be shared state between the per-peer goroutines.
	queryQuit := make(chan struct{})
	allQuit := make(chan struct{})
	var wg sync.WaitGroup
	msgChan := make(chan spMsg)
	subscription := spMsgSubscription{
		msgChan:  msgChan,
		quitChan: allQuit,
	}

	// Now we start a goroutine for each peer which manages the peer's
	// message subscription.
	peerQuits := make(map[string]chan struct{})
	for _, sp := range peers {
		sp.subscribeRecvMsg(subscription)
		wg.Add(1)
		peerQuits[sp.Addr()] = make(chan struct{})
		go func(sp *ServerPeer, peerQuit <-chan struct{}) {
			defer wg.Done()

			defer sp.unsubscribeRecvMsgs(subscription)

			for i := uint8(0); i < qo.numRetries; i++ {
				timeout := time.After(qo.timeout)
				sp.QueueMessageWithEncoding(queryMsg,
					nil, qo.encoding)
				select {
				case <-queryQuit:
					return
				case <-s.quit:
					return
				case <-peerQuit:
					return
				case <-timeout:
				}
			}
		}(sp, peerQuits[sp.Addr()])
	}

	// This goroutine will wait until all of the peer-query goroutines have
	// terminated, and then initiate a query shutdown.
	go func() {
		wg.Wait()

		// Make sure our main goroutine and the subscription know to
		// quit.
		close(allQuit)

		// Close the done channel, if any.
		if qo.doneChan != nil {
			close(qo.doneChan)
		}
	}()

	// Loop for any messages sent to us via our subscription channel and
	// check them for whether they satisfy the query. Break the loop when
	// allQuit is closed.
checkResponses:
	for {
		select {
		case <-queryQuit:
			break checkResponses

		case <-s.quit:
			break checkResponses

		case <-allQuit:
			break checkResponses

		// A message has arrived over the subscription channel, so we
		// execute the checkResponses callback to see if this ends our
		// query session.
		case sm := <-msgChan:
			// TODO: This will get stuck if checkResponse gets
			// stuck. This is a caveat for callers that should be
			// fixed before exposing this function for public use.
			select {
			case <-peerQuits[sm.sp.Addr()]:
			default:
				checkResponse(sm.sp, sm.msg, queryQuit,
					peerQuits[sm.sp.Addr()])
			}
		}
	}
}

// queryChainServicePeers is a helper function that sends a query to one or
// more peers of the given ChainService, and waits for an answer. The timeout
// for queries is set by the QueryTimeout package-level variable or the Timeout
// functional option.
func queryChainServicePeers(
	// s is the ChainService to use.
	s *ChainService,

	// queryMsg is the message to send to each peer selected by selectPeer.
	queryMsg wire.Message,

	// checkResponse is called for every message within the timeout period.
	// The quit channel lets the query know to terminate because the
	// required response has been found. This is done by closing the
	// channel.
	checkResponse func(sp *ServerPeer, resp wire.Message,
		quit chan<- struct{}) bool,

	// options takes functional options for executing the query.
	options ...QueryOption) {

	// Starting with the set of default options, we'll apply any specified
	// functional options to the query.
	qo := defaultQueryOptions()
	qo.applyQueryOptions(options...)

	// We get an initial view of our peers, to be updated each time a peer
	// query times out.
	queryPeer := s.blockManager.SyncPeer()
	peerTries := make(map[string]uint8)

	// This will be state used by the peer query goroutine.
	queryQuit := make(chan struct{})
	subQuit := make(chan struct{})

	// Increase this number to be able to handle more queries at once as
	// each channel gets results for all queries, otherwise messages can
	// get mixed and there's a vicious cycle of retries causing a bigger
	// message flood, more of which get missed.
	msgChan := make(chan spMsg)
	subscription := spMsgSubscription{
		msgChan:  msgChan,
		quitChan: subQuit,
	}

	// Loop for any messages sent to us via our subscription channel and
	// check them for whether they satisfy the query. Break the loop if
	// it's time to quit.
	peerTimeout := time.NewTimer(qo.timeout)
	connectionTimeout := time.NewTimer(qo.peerConnectTimeout)
	connectionTicker := connectionTimeout.C
	reqNum := atomic.AddUint32(&s.reqNum, 1)

	query := Query{
		ReqNum:     reqNum,
		Command:    queryMsg.Command(),
		Peer:       queryPeer,
		CreateTime: uint32(time.Now().Unix()),
	}
	s.mtxQueries.Lock()
	s.queries[reqNum] = &query
	s.mtxQueries.Unlock()

	reqName := fmt.Sprintf("%d/%s", reqNum, queryMsg.Command())
	if queryPeer != nil {
		peerTries[queryPeer.Addr()]++
		queryPeer.subscribeRecvMsg(subscription)
		queryPeer.QueueMessageWithEncoding(queryMsg, nil, qo.encoding)
		log.Tracef("[%s] sending to sync peer [%s]", reqName, queryPeer)
		query.LastRequestTime = uint32(time.Now().Unix())
	} else {
		log.Debugf("[%s] not sending because we have no sync peer", reqName)
	}
checkResponses:
	for {
		log.Tracef("[%s] waiting for replies or timeouts", reqName)
		select {
		case <-connectionTicker:
			// When we time out, we're done.
			if queryPeer != nil {
				queryPeer.unsubscribeRecvMsgs(subscription)
			}
			log.Debugf("[%s] connection timeout", reqName)
			break checkResponses

		case <-queryQuit:
			// Same when we get a quit signal.
			if queryPeer != nil {
				queryPeer.unsubscribeRecvMsgs(subscription)
			}
			log.Tracef("[%s] complete", reqName)
			break checkResponses

		case <-s.quit:
			// Same when chain server's quit is signaled.
			if queryPeer != nil {
				queryPeer.unsubscribeRecvMsgs(subscription)
			}
			log.Debugf("[%s] server shutdown", reqName)
			break checkResponses

		// A message has arrived over the subscription channel, so we
		// execute the checkResponses callback to see if this ends our
		// query session.
		case sm := <-msgChan:
			// TODO: This will get stuck if checkResponse gets
			// stuck. This is a caveat for callers that should be
			// fixed before exposing this function for public use.
			if checkResponse(sm.sp, sm.msg, queryQuit) {
				// cfilter messages are way too noisy
				log.Tracef("[%s] good reply [%s] from [%s]",
					reqName, sm.msg.Command(), sm.sp.String())
				query.LastResponseTime = uint32(time.Now().Unix())

				// Each time we receive a response from the current
				// peer, we'll reset the main peer timeout as they're
				// being responsive.
				if !peerTimeout.Stop() {
					select {
					case <-peerTimeout.C:
					default:
					}
				}
				peerTimeout.Reset(qo.timeout)
			}

			// Also at this point, if the peerConnectTimeout is
			// still active, then we can disable it, as we're
			// receiving responses from the current peer.
			if connectionTicker != nil && !connectionTimeout.Stop() {
				select {
				case <-connectionTimeout.C:
				default:
				}
			}
			connectionTicker = nil

		// The current peer we're querying has failed to answer the
		// query. Time to select a new peer and query it.
		case <-peerTimeout.C:
			oldQueryPeer := queryPeer
			queryPeer = nil
			for _, peer := range s.Peers() {
				// If the peer is no longer connected, we'll
				// skip them.
				if !peer.Connected() {
					continue
				}

				// If we've yet to try this peer, we'll make
				// sure to do so. If we've exceeded the number
				// of tries we should retry this peer, then
				// we'll skip them.
				numTries, ok := peerTries[peer.Addr()]
				if ok && numTries >= qo.numRetries {
					continue
				}

				queryPeer = peer

				if oldQueryPeer != nil {
					oldQueryPeer.unsubscribeRecvMsgs(subscription)
					log.Debugf("[%s] got timeout from [%s], querying [%s]",
						reqName, oldQueryPeer.String(), queryPeer.String())
				} else {
					log.Debugf("[%s] found a peer to query [%s]",
						reqName, queryPeer.String())
				}

				// Found a peer we can query.
				peerTries[queryPeer.Addr()]++
				queryPeer.subscribeRecvMsg(subscription)
				queryPeer.QueueMessageWithEncoding(
					queryMsg, nil, qo.encoding,
				)
				query.LastRequestTime = uint32(time.Now().Unix())
				query.Peer = queryPeer
				break
			}

			// If at this point, we don't yet have a query peer,
			// then we'll exit now as all the peers are exhausted.
			if queryPeer == nil {
				log.Debugf("[%s] no peers to query", reqName)
				break checkResponses
			}
			peerTimeout.Reset(qo.timeout)
		}
	}

	// Close the subscription quit channel and the done channel, if any.
	close(subQuit)
	peerTimeout.Stop()
	if qo.doneChan != nil {
		close(qo.doneChan)
	}
	s.mtxQueries.Lock()
	delete(s.queries, reqNum)
	s.mtxQueries.Unlock()
}

// getFilterFromCache returns a filter from ChainService's FilterCache if it
// exists, returning nil and error if it doesn't.
func (s *ChainService) getFilterFromCache(blockHash *chainhash.Hash) (*gcs.Filter, er.R) {

	cacheKey := cache.FilterCacheKey{BlockHash: *blockHash}

	filterValue, err := s.FilterCache.Get(cacheKey)
	if err != nil {
		return nil, err
	}

	return filterValue.(*cache.CacheableFilter).Filter, nil
}

// putFilterToCache inserts a given filter in ChainService's FilterCache.
func (s *ChainService) putFilterToCache(blockHash *chainhash.Hash, filter *gcs.Filter) (bool, er.R) {

	cacheKey := cache.FilterCacheKey{BlockHash: *blockHash}
	return s.FilterCache.Put(cacheKey, &cache.CacheableFilter{Filter: filter})
}

// cfiltersQuery is a struct that holds all the information necessary to
// perform batch GetCFilters request, and handle the responses.
type cfiltersQuery struct {
	filterType    wire.FilterType
	startHeight   int64
	stopHeight    int64
	stopHash      *chainhash.Hash
	filterHeaders []chainhash.Hash
	headerIndex   map[chainhash.Hash]int
	targetHash    chainhash.Hash
	filterChan    chan *gcs.Filter
	options       []QueryOption
}

// queryMsg returns the wire message to perform this query.
func (q *cfiltersQuery) queryMsg() wire.Message {
	return wire.NewMsgGetCFilters(
		q.filterType, uint32(q.startHeight), q.stopHash,
	)
}

const filterPrefetchDistance = wire.MaxGetCFiltersReqRange * 5
const filterBatchSize = wire.MaxGetCFiltersReqRange * 10

func (s *ChainService) prepareCFiltersQueries(
	blockHash chainhash.Hash,
	height int32,
	filterType wire.FilterType,
	options ...QueryOption,
) ([]*cfiltersQuery, er.R) {
	out := make([]*cfiltersQuery, 0, 10)
	for num := 0; num < filterBatchSize; num += wire.MaxGetCFiltersReqRange {
		x, err := s.prepareCFiltersQuery(blockHash, height, filterType, options...)
		if err != nil {
			if len(out) == 0 {
				return nil, err
			}
			log.Infof("Non-criticial error from prepareCFiltersQuery [%s]", err.String())
			break
		}
		out = append(out, x)
		height = int32(x.stopHeight + 1)
		if x.stopHeight < x.startHeight {
			height = int32(x.stopHeight - 1)
		}
		if bh, err := s.GetBlockHash(int64(height)); err != nil {
			// Maybe it's past the tip, not going to worry about this, return what we've got
			break
		} else {
			blockHash = *bh
		}
	}
	return out, nil
}

// prepareCFiltersQuery creates a cfiltersQuery that can be used to fetch a
// CFilter fo the given block hash.
func (s *ChainService) prepareCFiltersQuery(
	blockHash chainhash.Hash,
	height int32,
	filterType wire.FilterType,
	options ...QueryOption,
) (*cfiltersQuery, er.R) {

	bestBlock, err := s.BestBlock()
	if err != nil {
		return nil, er.Errorf("unable to get best block: %v", err)
	}
	bestHeight := int64(bestBlock.Height)

	qo := defaultQueryOptions()
	qo.applyQueryOptions(options...)

	// If the query specifies an optimistic batch we will attempt to fetch
	// the maximum number of filters in anticipation of calls for the
	// following or preceding filters.
	var startHeight, stopHeight int64

	switch qo.optimisticBatch {

	// No batching, the start and stop height will be the same.
	case noBatch:
		startHeight = int64(height)
		stopHeight = int64(height)

	// Forward batch, fetch as many of the following filters as possible.
	case forwardBatch:
		startHeight = int64(height)
		stopHeight = startHeight + wire.MaxGetCFiltersReqRange - 1

		// We need a longer timeout, since we are going to receive more
		// than a single response.
		options = append(options, Timeout(QueryBatchTimeout))

	// Reverse batch, fetch as many of the preceding filters as possible.
	case reverseBatch:
		stopHeight = int64(height)
		startHeight = stopHeight - wire.MaxGetCFiltersReqRange + 1

		// We need a longer timeout, since we are going to receive more
		// than a single response.
		options = append(options, Timeout(QueryBatchTimeout))
	}

	// Block 1 is the earliest one we can fetch.
	if startHeight < 1 {
		startHeight = 1
	}

	// If the stop height with the maximum batch size is above our best
	// known block, then we use the best block height instead.
	if stopHeight > bestHeight {
		stopHeight = bestHeight
	}

	stopHash, err := s.GetBlockHash(stopHeight)
	if err != nil {
		return nil, er.Errorf("unable to get hash for "+
			"stopHeight=%d: %v", stopHeight, err)
	}

	// In order to verify the authenticity of the received filters, we'll
	// fetch the block headers and filter headers in the range
	// [startHeight-1, stopHeight]. We go one below our startHeight since
	// the hash of the previous block is needed for validation.
	numFilters := uint32(stopHeight - startHeight + 1)
	blockHeaders, _, err := s.NeutrinoDB.FetchBlockHeaderAncestors(
		numFilters, stopHash,
	)
	if err != nil {
		return nil, er.Errorf("unable to get %d block header "+
			"ancestors for stopHash=%v: %v", numFilters,
			stopHash, err)
	}

	if len(blockHeaders) != int(numFilters)+1 {
		return nil, er.Errorf("expected %d block headers, got %d",
			numFilters+1, len(blockHeaders))
	}

	filterHeaders, _, err := s.NeutrinoDB.FetchFilterHeaderAncestors(
		numFilters, stopHash,
	)
	if err != nil {
		return nil, er.Errorf("unable to get %d filter header "+
			"ancestors for stopHash=%v: %v", numFilters, stopHash,
			err)
	}

	if len(filterHeaders) != int(numFilters)+1 {
		return nil, er.Errorf("expected %d filter headers, got %d",
			numFilters+1, len(filterHeaders))
	}

	// We create a header index such that we can easily index into our
	// header slices for a given block hash in the received response,
	// without consulting the database. This also keeps track of which
	// blocks we are still awaiting a response for. We start at index=1, as
	// 0 is for the block startHeight-1, which is only needed for
	// validation.
	headerIndex := make(map[chainhash.Hash]int, len(blockHeaders)-1)
	for i := 1; i < len(blockHeaders); i++ {
		block := blockHeaders[i]
		headerIndex[block.BlockHash()] = i
	}

	// We'll immediately respond to the caller with the requested filter
	// when it is received, so we make a channel to notify on when it's
	// ready.
	filterChan := make(chan *gcs.Filter, 1)

	return &cfiltersQuery{
		filterType:    filterType,
		startHeight:   startHeight,
		stopHeight:    stopHeight,
		stopHash:      stopHash,
		filterHeaders: filterHeaders,
		headerIndex:   headerIndex,
		targetHash:    blockHash,
		filterChan:    filterChan,
		options:       options,
	}, nil
}

// handleCFiltersRespons is called every time we receive a response for the
// GetCFilters request.
// Returns true if the reply is valid (related to the query) but we still need more
// closes the quit chan if we're done
func (s *ChainService) handleCFiltersResponse(q *cfiltersQuery,
	resp wire.Message, quit chan<- struct{}) bool {

	// We're only interested in "cfilter" messages.
	response, ok := resp.(*wire.MsgCFilter)
	if !ok {
		return false
	}

	// If the response doesn't match our request, ignore this message.
	if q.filterType != response.FilterType {
		return false
	}

	// If this filter is for a block not in our index, we can ignore it, as
	// we either already got it, or it is out of our queried range.
	i, ok := q.headerIndex[response.BlockHash]
	if !ok {
		// This happens CONSTANTLY because we query multiple ranges at the same time
		return false
	}

	gotFilter, err := gcs.FromNBytes(
		builder.DefaultP, builder.DefaultM, response.Data,
	)
	if err != nil {
		// Malformed filter data. We can ignore this message.
		log.Debugf("Malformed filter [%v]", response.BlockHash)
		return false
	}

	// Now that we have a proper filter, ensure that re-calculating the
	// filter header hash for the header _after_ the filter in the chain
	// checks out. If not, we can ignore this response.
	curHeader := q.filterHeaders[i]
	prevHeader := q.filterHeaders[i-1]
	gotHeader, err := builder.MakeHeaderForFilter(
		gotFilter, prevHeader,
	)
	if err != nil {
		log.Debugf("Error making header from filter [%v]: [%v]", response.BlockHash, err)
		return false
	}

	if gotHeader != curHeader {
		log.Debugf("Cfilter header mismatch [%v] (%v) (%v != %v)",
			response.BlockHash, i, gotHeader, curHeader)
		return false
	}

	// At this point, the filter matches what we know about it and we
	// declare it sane. If this is the filter requested initially, send it
	// to the caller immediately.
	if response.BlockHash == q.targetHash {
		q.filterChan <- gotFilter
	}

	// Put the filter in the cache and persistToDisk if the caller
	// requested it.
	// TODO(halseth): for an LRU we could take care to insert the next
	// height filter last.
	evict, errr := s.putFilterToCache(&response.BlockHash, gotFilter)
	if errr != nil {
		log.Warnf("Couldn't write filter to cache: %v", errr)
	}

	// TODO(halseth): dynamically increase/decrease the batch size to match
	// our cache capacity.
	numFilters := q.stopHeight - q.startHeight + 1
	if evict && s.FilterCache.Len() < int(numFilters) {
		log.Debugf("Items evicted from the cache with less "+
			"than %d elements. Consider increasing the "+
			"cache size...", numFilters)
	}

	qo := defaultQueryOptions()
	qo.applyQueryOptions(q.options...)

	// Finally, we can delete it from the headerIndex.
	delete(q.headerIndex, response.BlockHash)

	// If the headerIndex is empty, we got everything we wanted, and can
	// exit.
	if len(q.headerIndex) == 0 {
		close(quit)
	}
	return true
}

func (s *ChainService) doFilterRequest(
	blockHash chainhash.Hash,
	height int32,
	ft wire.FilterType,
	options []QueryOption,
) er.R {
	s.mtxCFilter.Lock()
	for pf := range s.pendingFilters {
		if height >= pf.bottomHeight && height <= pf.topHeight {
			s.mtxCFilter.Unlock()
			<-pf.ch
			return nil
		} else {
			log.Debugf("height %d < bottomHeight %d || > topHeight %d",
				height, pf.bottomHeight, pf.topHeight)
		}
	}

	// We didn't get the filter from the DB, so we'll try to get it from
	// the network.
	queries, err := s.prepareCFiltersQueries(blockHash, height, ft, options...)
	if err != nil {
		s.mtxCFilter.Unlock()
		return err
	}
	ch := make(chan struct{}, 1)
	bottomHeight := int32(queries[0].startHeight)
	topHeight := int32(queries[len(queries)-1].stopHeight)
	if bottomHeight > topHeight {
		topHeight, bottomHeight = bottomHeight, topHeight
	}
	pfr := pendingFiltersReq{
		ch: ch, bottomHeight: bottomHeight, topHeight: topHeight,
	}
	s.pendingFilters[&pfr] = struct{}{}

	s.mtxCFilter.Unlock()

	// With all the necessary items retrieved, we'll launch our concurrent
	// query to the set of connected peers.
	log.Debugf("Fetching filters for heights=[%v, %v]",
		bottomHeight, topHeight)

	var wg sync.WaitGroup
	for _, q := range queries {
		wg.Add(1)
		go func(query *cfiltersQuery) {
			queryChainServicePeers(
				s,
				// Send a wire.MsgGetCFilters
				query.queryMsg(),

				// Check responses and if we get one that matches, end
				// the query early.
				func(_ *ServerPeer, resp wire.Message, quit chan<- struct{}) bool {
					return s.handleCFiltersResponse(query, resp, quit)
				},
				query.options...,
			)

			// If there are elements left to receive, the query failed.
			if len(query.headerIndex) > 0 {
				numFilters := query.stopHeight - query.startHeight + 1
				log.Errorf("Query failed with %d out of %d filters "+
					"received", len(query.headerIndex), numFilters)
			}
			wg.Done()
		}(q)
	}

	wg.Wait()

	s.mtxCFilter.Lock()
	delete(s.pendingFilters, &pfr)
	close(ch)
	s.mtxCFilter.Unlock()
	return nil
}

func (s *ChainService) getMoreFiltersIfNeeded(
	blockHash chainhash.Hash,
	ft wire.FilterType,
	options []QueryOption,
) {
	if blockHash[0] != 0 {
		// We don't need to perform this check every single time
		return
	}
	qo := defaultQueryOptions()
	qo.applyQueryOptions(options...)
	if qo.optimisticBatch == noBatch {
		return
	}
	go func() {
		_, height, err := s.NeutrinoDB.FetchBlockHeader(&blockHash)
		if err != nil {
			// Error so we'll just do nothing
			return
		}
		baseHeight := height / filterPrefetchDistance * filterPrefetchDistance
		tryHeight := baseHeight + filterPrefetchDistance
		if qo.optimisticBatch == reverseBatch {
			tryHeight = baseHeight - filterPrefetchDistance
		}
		if bh, err := s.GetBlockHash(int64(tryHeight)); err != nil {
			// Don't worry, it's only a prefetch
		} else {
			//log.Debugf("Prefetching filter headers starting at height [%d]", tryHeight)
			s.GetCFilter(*bh, ft, options...)
		}
	}()
}

// GetCFilter gets a cfilter from the database. Failing that, it requests the
// cfilter from the network and writes it to the database. If extended is true,
// an extended filter will be queried for. Otherwise, we'll fetch the regular
// filter.
func (s *ChainService) GetCFilter(blockHash chainhash.Hash,
	filterType wire.FilterType, options ...QueryOption) (*gcs.Filter, er.R) {

	// The only supported filter atm is the regular filter, so we'll reject
	// all other filters.
	if filterType != wire.GCSFilterRegular {
		return nil, er.Errorf("unknown filter type: %v", filterType)
	}

	// First check the cache to see if we already have this filter. If
	// so, then we can return it an exit early.
	filter, err := s.getFilterFromCache(&blockHash)
	if err == nil && filter != nil {
		s.getMoreFiltersIfNeeded(blockHash, filterType, options)
		return filter, nil
	}
	if err != nil && !cache.ErrElementNotFound.Is(err) {
		return nil, err
	}

	doHash := &blockHash
	var doHeight int64
	if _, height, err := s.NeutrinoDB.FetchBlockHeader(&blockHash); err != nil {
		return nil, err
	} else if _, tipHeight, err := s.NeutrinoDB.BlockChainTip(); err != nil {
		return nil, err
	} else if tipHeight-height < wire.MaxGetCFiltersReqRange {
		// We're at or near the tip, don't load a batch
		doHeight = int64(height)
	} else {
		// Always load on even boundaries to prevent duplication
		tryHeight := int64(height) / filterBatchSize * filterBatchSize
		if tryHeight == 0 {
			// height 0 is unfetchable
			tryHeight++
		}
		if dh, err := s.GetBlockHash(tryHeight); err != nil {
			log.Debugf("Non-critical error getting hash at height [%d]: [%s]",
				doHeight, err.String())
			doHeight = int64(height)
		} else {
			doHash = dh
			doHeight = tryHeight
		}
	}

	for {
		if err := s.doFilterRequest(*doHash, int32(doHeight), filterType, options); err != nil {
			return nil, err
		}

		// Since another request might have added the filter to the cache while
		// we were waiting for the mutex, we do a final lookup before starting
		// our own query.
		filter, err = s.getFilterFromCache(&blockHash)
		if err == nil && filter != nil {
			return filter, nil
		}
		if err != nil && !cache.ErrElementNotFound.Is(err) {
			return nil, err
		}
		// Maybe the block was rolled back while we were fetching ?
		if _, _, err := s.NeutrinoDB.FetchBlockHeader(&blockHash); err != nil {
			return nil, err
		}
		log.Warnf("Made request for filter [%s] but still not in cache, retry",
			blockHash.String())
	}
}

// GetBlock gets a block by requesting it from the network, one peer at a
// time, until one answers. If the block is found in the cache, it will be
// returned immediately.
func (s *ChainService) GetBlock(blockHash chainhash.Hash,
	options ...QueryOption) (*btcutil.Block, er.R) {

	// Fetch the corresponding block header from the database. If this
	// isn't found, then we don't have the header for this block so we
	// can't request it.
	blockHeader, height, err := s.NeutrinoDB.FetchBlockHeader(&blockHash)
	if err != nil || blockHeader.BlockHash() != blockHash {
		return nil, er.Errorf("Couldn't get header for block %s "+
			"from database", blockHash)
	}
	return s.GetBlock0(blockHash, height, options...)
}

func (s *ChainService) GetBlock0(blockHash chainhash.Hash, height uint32,
	options ...QueryOption) (*btcutil.Block, er.R) {

	// Starting with the set of default options, we'll apply any specified
	// functional options to the query so that we can check what inv type
	// to use.
	qo := defaultQueryOptions()
	qo.applyQueryOptions(options...)
	invType := wire.InvTypeWitnessBlock
	if qo.encoding == wire.BaseEncoding {
		invType = wire.InvTypeBlock
	}

	// Create an inv vector for getting this block.
	inv := wire.NewInvVect(invType, &blockHash)

	// If the block is already in the cache, we can return it immediately.
	blockValue, err := s.BlockCache.Get(*inv)
	if err == nil && blockValue != nil {
		return blockValue.(*cache.CacheableBlock).Block, err
	}
	if err != nil && !cache.ErrElementNotFound.Is(err) {
		return nil, err
	}

	// Construct the appropriate getdata message to fetch the target block.
	getData := wire.NewMsgGetData()
	getData.AddInvVect(inv)

	// The block is only updated from the checkResponse function argument,
	// which is always called single-threadedly. We don't check the block
	// until after the query is finished, so we can just write to it
	// naively.
	var foundBlock *btcutil.Block
	queryChainServicePeers(
		s,
		// Send a wire.GetDataMsg
		getData,

		// Check responses and if we get one that matches, end the
		// query early.
		func(sp *ServerPeer, resp wire.Message, quit chan<- struct{}) bool {
			switch response := resp.(type) {
			// We're only interested in "block" messages.
			case *wire.MsgBlock:
				// Only keep this going if we haven't already
				// found a block, or we risk closing an already
				// closed channel.
				if foundBlock != nil {
					return false
				}

				// If this isn't our block, ignore it.
				if response.BlockHash() != blockHash {
					return false
				}
				block := btcutil.NewBlock(response)

				// Only set height if btcutil hasn't
				// automagically put one in.
				if block.Height() == btcutil.BlockHeightUnknown {
					block.SetHeight(int32(height))
				}

				// If this claims our block but doesn't pass
				// the sanity check, the peer is trying to
				// bamboozle us. Disconnect it.
				if err := blockchain.CheckBlockSanity(
					block,
					// We don't need to check PoW because
					// by the time we get here, it's been
					// checked during header
					// synchronization
					s.chainParams.PowLimit,
					s.timeSource,
				); err != nil {
					log.Warnf("Invalid block for %s "+
						"received from %s -- "+
						"disconnecting peer", blockHash,
						sp.Addr())
					sp.Disconnect()
					return false
				}

				// TODO(roasbeef): modify CheckBlockSanity to
				// also check witness commitment

				// At this point, the block matches what we
				// know about it and we declare it sane. We can
				// kill the query and pass the response back to
				// the caller.
				foundBlock = block
				close(quit)
				return true
			default:
			}
			return false
		},
		options...,
	)
	if foundBlock == nil {
		return nil, er.Errorf("Couldn't retrieve block %s from "+
			"network", blockHash)
	}

	// Add block to the cache before returning it.
	_, err = s.BlockCache.Put(*inv, &cache.CacheableBlock{Block: foundBlock})
	if err != nil {
		log.Warnf("couldn't write block to cache: %v", err)
	}

	return foundBlock, nil
}

// SendTransaction0 sends a transaction to your peers. It returns an error if
// it is "unlikely" that the network has accepted it.
//
// Fasten your seatbelts because here comes the stupid.
// So, the way it works is this: you create a transaction and you want to bcast it
// and see if "most of the network" accepts it, like at least to check that it's
// not going to be immediately dropped on the ground because you were trying to spend
// a txo which was already spent or something. So you would want something like an HTTP
// endpoint which replies yay or nay. And I'm here to tell you that Bitcoin protocol
// does not offer you anything of the sort.
//
// In Bitcoin, what you need to do is send an INV message ("I have a thing"), which then
// after receiving it, a node might reply with getdata ("gimme dat"), and only then can
// you send the tx to the node. If the node is unhappy with your tx, it MIGHT send you
// back a Reject message, but this is a bit deprecated and the Satoshi client doesn't
// do it and if the node crashes or something, it obviously won't send a reject.
//
// If the node DOES like your tx, it will store it in it's mempool and then start
// sending INV messages to its other peers, but it WONT send an INV back to you.
// So waiting to see if other nodes send you an INV of your transaction is a good way
// to know that it's being accepted, but you have to be careful because as you send the
// tx to each node in turn, each node will then become unwilling to give you an INV
// message with the tx.
//
// The algorithm used here is as follows:
// * Send the tx to your syncNode (the one you are primarily using)
// * If you receive an INV message from anyone then consider it good
// * Otherwise, if you receive a reject message from anyone then consider it bad
// * Otherwise, if you have notificed every one of your peers and received a getdata
//     request from all of them, then consider it good. This last rule covers the case
//     when you are connected only to one peer using --connect
//
// NOTE: If you get an error RejMempool or RejConfirmed, it means your transaction
//       has already been accepted and you're just getting notified that the node already
//       knows about it.
func (s *ChainService) SendTransaction0(tx *wire.MsgTx, options ...QueryOption) er.R {
	qo := defaultQueryOptions()
	qo.applyQueryOptions(options...)
	invType := wire.InvTypeWitnessTx
	if qo.encoding == wire.BaseEncoding {
		invType = wire.InvTypeTx
	}

	// Create an inv.
	txHash := tx.TxHash()
	inv := wire.NewMsgInv()
	iv := wire.NewInvVect(invType, &txHash)
	inv.AddInvVect(iv)

	gotInv := false
	var reject er.R
	peersGetdatad := 0

	log.Debugf("Listening for INV [%v] [%s/%v]", iv, iv.Hash, iv.Type)

	var doneCh chan<- struct{}
	stopCh := make(chan struct{})
	go func() {
		sch := stopCh
		invCh := s.ListenInvs(txHash)
		defer s.StopListenInvs(txHash, invCh)
	lewp:
		for {
			select {
			case <-sch:
				break lewp
			case sp := <-invCh:
				log.Debugf("Got INV from [%s]", sp)
				gotInv = true
				break lewp
			}
		}
		for {
			if doneCh != nil {
				close(doneCh)
				return
			}
			time.Sleep(time.Millisecond * 50)
		}
	}()

	// Send the peer query and listen for getdata.
	queryChainServicePeers(
		s,
		inv,
		func(sp *ServerPeer,
			resp wire.Message,
			quit chan<- struct{},
		) bool {
			if doneCh == nil {
				doneCh = quit
			}
			switch response := resp.(type) {
			// A peer has replied with a GetData message, so we'll
			// send them the transaction.
			case *wire.MsgGetData:
				for _, vec := range response.InvList {
					if vec.Hash == txHash {
						sp.QueueMessageWithEncoding(tx, nil, qo.encoding)
					}
				}
				peersGetdatad++
				return true

			// A peer has rejected our transaction for whatever
			// reason. Rather than returning to the caller upon the
			// first rejection, we'll gather them all to determine
			// whether it is critical/fatal.
			case *wire.MsgReject:
				// Ensure this rejection is for the transaction
				// we're attempting to broadcast.
				if response.Hash != txHash {
					return false
				}
				reject = pushtx.ParseBroadcastError(response, sp.Addr())
			}
			return false
		},
	)
	if stopCh != nil {
		close(stopCh)
	}

	if gotInv {
		log.Debugf("Tx [%s] got an inv", txHash)
		return nil
	} else if reject != nil {
		log.Debugf("Tx [%s] got rejected [%s]", txHash, reject)
		return reject
	}

	log.Debugf("Tx [%s] no rejects and [%d] of [%d] peers sent a getdata",
		txHash, peersGetdatad, len(s.Peers()))
	if peersGetdatad >= len(s.Peers()) {
		return nil
	} else {
		return er.Errorf("No INV messages and only [%d] of our [%d] peers sent a getData",
			peersGetdatad, len(s.Peers()))
	}
}
